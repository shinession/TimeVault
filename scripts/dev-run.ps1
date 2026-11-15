<#
.SYNOPSIS
  Builds and runs TimeVault for local development against the private dev PostgreSQL.

.DESCRIPTION
  1. Starts the private PostgreSQL cluster (scripts\dev-db.ps1 start).
  2. Builds the server into <repo>\data\dev\bin.
  3. On the first run creates <repo>\data\dev with a source, repository and restore folder,
     a random admin password (saved to data\dev\admin-password.txt, never printed) and a
     server.env file. Everything lives under the git-ignored data\ folder.
  4. Runs the server in the foreground on 127.0.0.1. Press Ctrl+C to stop it.

  If the Everything instance from scripts\dev-everything.ps1 has written data\everything\everything.env,
  its settings are loaded too, so search works without further steps.

  The Go toolchain must be on PATH, or point TIMEVAULT_GO_ROOT at the Go installation folder.

.PARAMETER Port
  TCP port on 127.0.0.1 (default 8470).
#>
[CmdletBinding()]
param([int]$Port = 8470)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$devDir = Join-Path $repoRoot 'data\dev'
$binDir = Join-Path $devDir 'bin'
$exe = Join-Path $binDir 'timevault-server.exe'
$serverEnv = Join-Path $devDir 'server.env'
$passwordFile = Join-Path $devDir 'admin-password.txt'

if ($env:TIMEVAULT_GO_ROOT -and -not (Get-Command go -ErrorAction SilentlyContinue)) {
    $env:Path = (Join-Path $env:TIMEVAULT_GO_ROOT 'bin') + ';' + $env:Path
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'The go command was not found. Add Go to PATH or set TIMEVAULT_GO_ROOT.'
}

& powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'dev-db.ps1') start
if ($LASTEXITCODE -ne 0) { throw 'Could not start the private PostgreSQL.' }

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

. (Join-Path $PSScriptRoot 'dev-env.ps1')
$env:TIMEVAULT_LISTEN_ADDR = "127.0.0.1:$Port"

Write-Host "TimeVault: http://127.0.0.1:$Port/  (user 'admin', password in $passwordFile)"
Write-Host "Demo source folder you can register in the UI: $(Join-Path $devDir 'source')"
& $exe serve
