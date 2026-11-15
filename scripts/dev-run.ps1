<#
.SYNOPSIS
  Builds and runs TimeVault for local development.

.DESCRIPTION
  <repo>\.env is the only settings file: the service reads it, and so does every dev-* script (see
  scripts\dotenv.ps1). This script

  1. creates .env from a commented template when it is missing, and moves the settings of the files
     older versions used (data\local.env, data\pgdev\db.env, data\dev\server.env,
     data\everything\everything.env) into it;
  2. starts the private PostgreSQL cluster (scripts\dev-db.ps1 start), unless TIMEVAULT_DATABASE_URL
     in .env points to another PostgreSQL server, such as the one installed on this computer. In that
     case nothing is started: the database must already be running;
  3. builds the server into <repo>\data\dev\bin;
  4. adds to .env what the service needs and .env does not have yet: a repository and a restore folder
     under <repo>\data\dev, TIMEVAULT_AUTO_MIGRATE=true, a session key and, on the very first run, a
     random password for the local administrator 'admin' (saved to data\dev\admin-password.txt, never
     printed; only its hash goes into .env);
  5. runs the server in the foreground. Press Ctrl+C to stop it.

  A TIMEVAULT_* variable in your terminal would override .env, silently. They are removed, and named,
  before the server starts, so what .env says is what you get. -Port starts one run on another port.

  Anyone can browse; backing up, restoring, the audit log and the settings need an account: the
  local administrator, or a company account from the Redis user directory (TIMEVAULT_REDIS_URL).

  If the Everything instance from scripts\dev-everything.ps1 was started, it wrote its settings into
  .env as well, so search works without further steps.

  The Go toolchain must be on PATH, or TIMEVAULT_GO_ROOT (in .env) must name the Go installation
  folder (the one that contains bin\go.exe).

.PARAMETER Port
  TCP port on 127.0.0.1 for this run only. Without it the server listens on TIMEVAULT_LISTEN_ADDR
  from .env, which is 127.0.0.1:8660 by default.
#>
[CmdletBinding()]
param([int]$Port)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$devDir = Join-Path $repoRoot 'data\dev'
$binDir = Join-Path $devDir 'bin'
$exe = Join-Path $binDir 'timevault-server.exe'
$passwordFile = Join-Path $devDir 'admin-password.txt'
$privateDbPort = '55432'

. (Join-Path $PSScriptRoot 'dotenv.ps1')
Initialize-DotEnv
Move-LegacyEnvFiles

Add-GoToPath
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $goRoot = if ($env:TIMEVAULT_GO_ROOT) { $env:TIMEVAULT_GO_ROOT } else { Get-DotEnvValue 'TIMEVAULT_GO_ROOT' }
    $cause = if ($goRoot) { "TIMEVAULT_GO_ROOT is '$goRoot' but that folder has no bin\go.exe" } else { 'TIMEVAULT_GO_ROOT is not set' }
    throw "The go command was not found ($cause). Add Go to PATH, or put the line TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe> into $DotEnvFile."
}

$dbUrl = Get-DotEnvValue 'TIMEVAULT_DATABASE_URL'
$dbHost, $dbPort = $null, $null
if ($dbUrl -match '^postgres(?:ql)?://[^@/]*@([^/:?#]+)(?::(\d+))?') { $dbHost, $dbPort = $Matches[1], $Matches[2] }
if ($dbUrl -and $dbPort -ne $privateDbPort) {
    $where = if (-not $dbHost) { 'the address in TIMEVAULT_DATABASE_URL' } elseif ($dbPort) { "${dbHost}:${dbPort}" } else { $dbHost }
    Write-Host "Using the PostgreSQL server at $where (TIMEVAULT_DATABASE_URL in .env). The private cluster is not started."
}
else {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'dev-db.ps1') start
    if ($LASTEXITCODE -ne 0) { throw 'Could not start the private PostgreSQL.' }
}

New-Item -ItemType Directory -Force -Path $binDir, (Join-Path $devDir 'source') | Out-Null

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

# What the service needs and .env does not have yet. A value that is already there, even an empty
# one, is never changed.
$defaults = [ordered]@{
    TIMEVAULT_REPOSITORY_PATH = Join-Path $devDir 'repo'
    TIMEVAULT_RESTORE_ROOT    = Join-Path $devDir 'restore'
    TIMEVAULT_AUTO_MIGRATE    = 'true'
}
foreach ($key in $defaults.Keys) {
    if ($null -eq (Get-DotEnvValue $key)) { Set-DotEnvValue $key $defaults[$key] }
}
foreach ($key in 'TIMEVAULT_REPOSITORY_PATH', 'TIMEVAULT_RESTORE_ROOT') {
    $folder = Get-DotEnvValue $key
    if ($folder -and $folder -eq $defaults[$key]) { New-Item -ItemType Directory -Force -Path $folder | Out-Null }
}
if ($null -eq (Get-DotEnvValue 'TIMEVAULT_SESSION_KEY')) {
    $keyBytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($keyBytes)
    Set-DotEnvValue 'TIMEVAULT_SESSION_KEY' ([Convert]::ToBase64String($keyBytes))
}

# The first run creates the local administrator. The password file marks that it was done, so an
# administrator that was left out of .env on purpose does not come back.
if (-not (Test-Path -LiteralPath $passwordFile) -and $null -eq (Get-DotEnvValue 'TIMEVAULT_ADMIN_PASSWORD_HASH')) {
    $password = New-RandomText 20
    $hash = ($password | & $exe hash-password).Trim()
    if (-not $hash.StartsWith('pbkdf2-sha256$')) { throw 'Could not create the admin password hash.' }
    Set-DotEnvValue 'TIMEVAULT_ADMIN_USER' 'admin'
    Set-DotEnvValue 'TIMEVAULT_ADMIN_PASSWORD_HASH' $hash
    Set-Content -LiteralPath $passwordFile -Value $password -NoNewline -Encoding ascii
    Write-Host "Created the development admin account 'admin'. The password is stored in: $passwordFile"
}

# A TIMEVAULT_* variable would override .env. Scripts run in your session, so one that an older
# dev-run.ps1 left behind is still there, and the server would listen on its port, not on the one
# written in .env.
$stale = Remove-SettingsFromEnvironment
if ($stale.Count -gt 0) {
    Write-Host ('Removed from this terminal, where they would override .env: ' + ($stale -join ', '))
    Write-Host '  (An older dev-run.ps1 left them behind. If they come back, they are set in your Windows user or system environment.)'
}
$env:TIMEVAULT_ENV_FILE = $DotEnvFile
if ($PSBoundParameters.ContainsKey('Port')) { $env:TIMEVAULT_LISTEN_ADDR = "127.0.0.1:$Port" }

$listen = if ($env:TIMEVAULT_LISTEN_ADDR) { $env:TIMEVAULT_LISTEN_ADDR } else { Get-DotEnvValue 'TIMEVAULT_LISTEN_ADDR' }
if (-not $listen) { $listen = '127.0.0.1:8660' }
$browse = 'http://' + ($listen -replace '^(0\.0\.0\.0|\[::\]|::)(?=:)', '127.0.0.1') + '/'
$adminUser = Get-DotEnvValue 'TIMEVAULT_ADMIN_USER'
if (-not $adminUser) { $adminUser = 'admin' }
$admin = if (Get-DotEnvValue 'TIMEVAULT_ADMIN_PASSWORD_HASH') { "'$adminUser', password in $passwordFile" } else { 'none (there is no TIMEVAULT_ADMIN_PASSWORD_HASH in .env)' }
$redis = if (Get-DotEnvValue 'TIMEVAULT_REDIS_URL') { 'configured' } else { "NOT configured, so only the local administrator can sign in. Put TIMEVAULT_REDIS_URL into $DotEnvFile" }

Write-Host "TimeVault: $browse  (anyone can browse; sign in to back up, restore or change settings)"
Write-Host "  Settings file (the only one): $DotEnvFile"
Write-Host "  Local administrator: $admin"
Write-Host "  Company accounts (Redis): $redis"
Write-Host "  Monitored folders: the Settings page (administrators). A demo folder to add there: $(Join-Path $devDir 'source')"
try { & $exe serve }
finally {
    # The script runs in your session: what it set must not stay and override .env the next time.
    Remove-Item -Path Env:TIMEVAULT_ENV_FILE, Env:TIMEVAULT_LISTEN_ADDR -ErrorAction SilentlyContinue
}
