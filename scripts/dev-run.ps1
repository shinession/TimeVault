<#
.SYNOPSIS
  Builds and runs TimeVault for local development.

.DESCRIPTION
  1. Starts the private PostgreSQL cluster (scripts\dev-db.ps1 start), unless the database settings
     (data\pgdev\db.env or .env) point to another PostgreSQL server, such as the one installed on
     this computer. In that case nothing is started: the database must already be running.
  2. Builds the server into <repo>\data\dev\bin.
  3. On the first run creates <repo>\data\dev with a demo source folder, a repository and a restore
     folder, a random password for the local administrator 'admin' (saved to
     data\dev\admin-password.txt, never printed) and a server.env file. Everything lives under the
     git-ignored data\ folder.
  4. Creates <repo>\.env from a commented template when it is missing. That is the service's own
     settings file (git-ignored); put TIMEVAULT_REDIS_URL there so company accounts can sign in.
  5. Runs the server in the foreground. Press Ctrl+C to stop it.

  Anyone can browse; backing up, restoring, the audit log and the settings need an account: the
  local administrator, or a company account from the Redis user directory.

  If the Everything instance from scripts\dev-everything.ps1 has written data\everything\everything.env,
  its settings are loaded too, so search works without further steps.

  The Go toolchain must be on PATH, or TIMEVAULT_GO_ROOT must name the Go installation folder (the
  one that contains bin\go.exe). To keep that across terminals without changing PATH, put the line
  TIMEVAULT_GO_ROOT=<folder> into data\local.env, which is loaded before Go is looked up.

.PARAMETER Port
  TCP port on 127.0.0.1. Without it the server listens on TIMEVAULT_LISTEN_ADDR from .env,
  which is 127.0.0.1:8660 by default.
#>
[CmdletBinding()]
param([int]$Port)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$devDir = Join-Path $repoRoot 'data\dev'
$binDir = Join-Path $devDir 'bin'
$exe = Join-Path $binDir 'timevault-server.exe'
$serverEnv = Join-Path $devDir 'server.env'
$passwordFile = Join-Path $devDir 'admin-password.txt'
$dotEnv = Join-Path $repoRoot '.env'
$privateDbPort = '55432'

# Besides loading the saved settings, dev-env.ps1 puts TIMEVAULT_GO_ROOT\bin on PATH when go is missing.
. (Join-Path $PSScriptRoot 'dev-env.ps1')
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $cause = if ($env:TIMEVAULT_GO_ROOT) { "TIMEVAULT_GO_ROOT is '$($env:TIMEVAULT_GO_ROOT)' but that folder has no bin\go.exe" } else { 'TIMEVAULT_GO_ROOT is not set' }
    throw "The go command was not found ($cause). Add Go to PATH, or put the line TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe> into data\local.env."
}

if (-not (Test-Path -LiteralPath $dotEnv)) {
    $template = @(
        '# TimeVault settings for development. The service reads this file when it starts',
        '# (scripts\dev-run.ps1 points it here). Real environment variables win over it, and dev-run.ps1',
        '# first loads data\pgdev\db.env, data\dev\server.env, data\everything\everything.env and',
        '# data\local.env into the environment, so a setting made in those files cannot be changed here.',
        '#',
        '# Company accounts come from the user directory in Redis (the app_user_* hashes). Without it',
        "# only the local administrator 'admin' can sign in (password: data\dev\admin-password.txt).",
        '# Fill in the URL (rediss:// for TLS), then check it from the repository folder with:',
        '#   data\dev\bin\timevault-server.exe check-redis-users',
        '# TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0',
        '#',
        '# Listen address. The default is 127.0.0.1:8660. Other computers need a TLS reverse proxy and',
        '# TIMEVAULT_ALLOW_NON_LOOPBACK=true as well.',
        '# TIMEVAULT_LISTEN_ADDR=127.0.0.1:8660'
    )
    Set-Content -LiteralPath $dotEnv -Value $template -Encoding ascii
    Write-Host "Created $dotEnv (the service's own settings file). Put TIMEVAULT_REDIS_URL there to let company accounts sign in."
}

# The last assignment of a key in .env, or $null. Comments are ignored.
function Get-DotEnvValue([string]$Key) {
    $found = $null
    foreach ($line in Get-Content -LiteralPath $dotEnv) {
        if ($line -match ('^\s*(?:export\s+)?' + [regex]::Escape($Key) + '\s*=\s*(.*?)\s*$')) { $found = $Matches[1].Trim('"').Trim("'") }
    }
    return $found
}

$dbUrl = if ($env:TIMEVAULT_DATABASE_URL) { $env:TIMEVAULT_DATABASE_URL } else { Get-DotEnvValue 'TIMEVAULT_DATABASE_URL' }
$dbHost, $dbPort = $null, $null
if ($dbUrl -match '^postgres(?:ql)?://[^@/]*@([^/:?#]+)(?::(\d+))?') { $dbHost, $dbPort = $Matches[1], $Matches[2] }
if ($dbUrl -and $dbPort -ne $privateDbPort) {
    $where = if (-not $dbHost) { 'the address in the database settings' } elseif ($dbPort) { "${dbHost}:${dbPort}" } else { $dbHost }
    Write-Host "Using the PostgreSQL server at $where (from the database settings). The private cluster is not started."
}
else {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'dev-db.ps1') start
    if ($LASTEXITCODE -ne 0) { throw 'Could not start the private PostgreSQL.' }
}

New-Item -ItemType Directory -Force -Path $binDir, (Join-Path $devDir 'source'), (Join-Path $devDir 'repo'), (Join-Path $devDir 'restore') | Out-Null

Push-Location $repoRoot
try {
    go build -o $exe ./cmd/timevault-server
    if ($LASTEXITCODE -ne 0) { throw 'go build failed.' }
}
finally { Pop-Location }

function New-RandomText([int]$Length) {
    $chars = (48..57) + (65..90) + (97..122)
    $bytes = New-Object byte[] $Length
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    -join ($bytes | ForEach-Object { [char]$chars[$_ % $chars.Count] })
}

if (-not (Test-Path $serverEnv)) {
    $password = New-RandomText 20
    $hash = ($password | & $exe hash-password).Trim()
    if (-not $hash.StartsWith('pbkdf2-sha256$')) { throw 'Could not create the admin password hash.' }
    $keyBytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($keyBytes)
    $lines = @(
        "TIMEVAULT_REPOSITORY_PATH=$(Join-Path $devDir 'repo')",
        "TIMEVAULT_RESTORE_ROOT=$(Join-Path $devDir 'restore')",
        'TIMEVAULT_ADMIN_USER=admin',
        "TIMEVAULT_ADMIN_PASSWORD_HASH=$hash",
        "TIMEVAULT_SESSION_KEY=$([Convert]::ToBase64String($keyBytes))",
        'TIMEVAULT_AUTO_MIGRATE=true'
    )
    Set-Content -LiteralPath $serverEnv -Value $lines -Encoding ascii
    Set-Content -LiteralPath $passwordFile -Value $password -NoNewline -Encoding ascii
    Write-Host "Created the development admin account 'admin'. The password is stored in: $passwordFile"
}

# Load again: server.env may have been created just now.
. (Join-Path $PSScriptRoot 'dev-env.ps1')
if (-not $env:TIMEVAULT_ENV_FILE) { $env:TIMEVAULT_ENV_FILE = $dotEnv }
if ($PSBoundParameters.ContainsKey('Port')) { $env:TIMEVAULT_LISTEN_ADDR = "127.0.0.1:$Port" }

$listen = if ($env:TIMEVAULT_LISTEN_ADDR) { $env:TIMEVAULT_LISTEN_ADDR } else { Get-DotEnvValue 'TIMEVAULT_LISTEN_ADDR' }
if (-not $listen) { $listen = '127.0.0.1:8660' }
$browse = 'http://' + ($listen -replace '^(0\.0\.0\.0|\[::\]|::)(?=:)', '127.0.0.1') + '/'
$redis = if ($env:TIMEVAULT_REDIS_URL -or (Get-DotEnvValue 'TIMEVAULT_REDIS_URL')) { 'configured' } else { "NOT configured, so only the local administrator can sign in. Put TIMEVAULT_REDIS_URL into $dotEnv" }

Write-Host "TimeVault: $browse  (anyone can browse; sign in to back up, restore or change settings)"
Write-Host "  Local administrator: 'admin', password in $passwordFile"
Write-Host "  Company accounts (Redis): $redis"
Write-Host "  Monitored folders: the Settings page (administrators). A demo folder to add there: $(Join-Path $devDir 'source')"
& $exe serve
