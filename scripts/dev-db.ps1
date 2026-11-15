<#
.SYNOPSIS
  Private PostgreSQL instance for TimeVault development and integration tests.

.DESCRIPTION
  Creates and runs a throw-away cluster under <repo>\data\pgdev using the PostgreSQL
  binaries that are already installed. It listens on 127.0.0.1 only, on its own port,
  and never touches an existing PostgreSQL service. The superuser password is random,
  stored only in <repo>\.env (git-ignored; TIMEVAULT_DATABASE_URL and TIMEVAULT_TEST_DATABASE_URL)
  and never printed.

  Durability is relaxed (fsync off) on purpose: this cluster is for development only.

  TIMEVAULT_DATABASE_URL in .env may point at another PostgreSQL server (for example the one
  installed on this computer). The scripts then never overwrite or delete that setting: creating the
  private cluster and reset stop with an explanation until you remove the two URLs from .env.

.PARAMETER Action
  init   Create the cluster and the databases 'timevault' and 'timevault_test'.
  start  Start the cluster (runs init first when needed).
  stop   Stop the cluster.
  status Show whether the cluster accepts connections.
  reset  Stop and delete the cluster, then create a new one.

.PARAMETER PgBin
  Folder with pg_ctl.exe, initdb.exe, psql.exe. Defaults to $env:TIMEVAULT_PG_BIN, then
  TIMEVAULT_PG_BIN in .env, then pg_ctl on PATH, then the newest C:\Program Files\PostgreSQL\<version>\bin.

.PARAMETER Port
  TCP port of the private cluster (default 55432).
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('init', 'start', 'stop', 'status', 'reset')]
    [string]$Action,
    [string]$PgBin,
    [int]$Port = 55432
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$dataDir = Join-Path $repoRoot 'data\pgdev'
$pgData = Join-Path $dataDir 'pgdata'
$logFile = Join-Path $dataDir 'postgres.log'
$dbUser = 'timevault'

. (Join-Path $PSScriptRoot 'dotenv.ps1')
Move-LegacyEnvFiles

function Resolve-PgBin {
    if ($PgBin) { return $PgBin }
    if ($env:TIMEVAULT_PG_BIN) { return $env:TIMEVAULT_PG_BIN }
    $fromEnvFile = Get-DotEnvValue 'TIMEVAULT_PG_BIN'
    if ($fromEnvFile) { return $fromEnvFile }
    $cmd = Get-Command pg_ctl.exe -ErrorAction SilentlyContinue
    if ($cmd) { return Split-Path -Parent $cmd.Source }
    $candidate = Get-ChildItem 'C:\Program Files\PostgreSQL' -Directory -ErrorAction SilentlyContinue |
        Sort-Object { [int]($_.Name -replace '\D', '') } -Descending |
        ForEach-Object { Join-Path $_.FullName 'bin' } |
        Where-Object { Test-Path (Join-Path $_ 'pg_ctl.exe') } |
        Select-Object -First 1
    if ($candidate) { return $candidate }
    throw 'PostgreSQL binaries not found. Pass -PgBin or set TIMEVAULT_PG_BIN.'
}

$bin = Resolve-PgBin
$pgCtl = Join-Path $bin 'pg_ctl.exe'
$initdb = Join-Path $bin 'initdb.exe'
$psql = Join-Path $bin 'psql.exe'
$pgIsReady = Join-Path $bin 'pg_isready.exe'

function Test-Running {
    & $pgIsReady -h 127.0.0.1 -p $Port -q 2>$null
    return ($LASTEXITCODE -eq 0)
}

function New-Password {
    $chars = (48..57) + (65..90) + (97..122)
    $bytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    -join ($bytes | ForEach-Object { [char]$chars[$_ % $chars.Count] })
}

function Read-Password {
    $url = Get-DotEnvValue 'TIMEVAULT_DATABASE_URL'
    if ($url -match '://[^:]+:([^@]+)@') { return $Matches[1] }
    throw "Cannot read the database password from TIMEVAULT_DATABASE_URL in $DotEnvFile"
}

# TIMEVAULT_DATABASE_URL may have been changed to point at another PostgreSQL server, for example
# the one installed on this computer. It is then the developer's setting: never overwrite it and
# never delete it.
function Test-EnvPointsElsewhere {
    $url = Get-DotEnvValue 'TIMEVAULT_DATABASE_URL'
    if (-not $url) { return $false }
    if ($url -match '@[^/:?#]+:(\d+)') { return ([int]$Matches[1] -ne $Port) }
    return $true
}

function Invoke-Psql([string]$Database, [string]$Sql) {
    $env:PGPASSWORD = Read-Password
    try {
        & $psql -h 127.0.0.1 -p $Port -U $dbUser -d $Database -v ON_ERROR_STOP=1 -q -t -A -c $Sql
        if ($LASTEXITCODE -ne 0) { throw "psql failed ($LASTEXITCODE)" }
    }
    finally { Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue }
}

function Initialize-Cluster {
    if (Test-Path (Join-Path $pgData 'PG_VERSION')) { return }
    if (Test-EnvPointsElsewhere) {
        throw "TIMEVAULT_DATABASE_URL in $DotEnvFile points to another PostgreSQL server, and creating the private cluster would replace it. Remove TIMEVAULT_DATABASE_URL and TIMEVAULT_TEST_DATABASE_URL from .env first."
    }
    New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
    $password = New-Password
    $pwFile = Join-Path $dataDir 'initdb.pw'
    Set-Content -LiteralPath $pwFile -Value $password -NoNewline -Encoding ascii
    try {
        & $initdb -D $pgData -U $dbUser -A scram-sha-256 --pwfile=$pwFile -E UTF8 --locale=C | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "initdb failed ($LASTEXITCODE)" }
    }
    finally { Remove-Item -LiteralPath $pwFile -Force -ErrorAction SilentlyContinue }

    $conf = @(
        '',
        '# --- TimeVault development cluster (not for production) ---',
        "port = $Port",
        "listen_addresses = '127.0.0.1'",
        'max_connections = 60',
        'fsync = off',
        'synchronous_commit = off',
        'full_page_writes = off'
    )
    Add-Content -LiteralPath (Join-Path $pgData 'postgresql.conf') -Value $conf -Encoding ascii

    $urlBase = "postgres://${dbUser}:${password}@127.0.0.1:${Port}"
    Set-DotEnvValue 'TIMEVAULT_DATABASE_URL' "$urlBase/timevault?sslmode=disable"
    Set-DotEnvValue 'TIMEVAULT_TEST_DATABASE_URL' "$urlBase/timevault_test?sslmode=disable"
}

function Invoke-PgCtl([string]$Arguments) {
    # Start-Process (ShellExecute) keeps the long-running postgres child from inheriting this
    # process' stdout pipe. Start-Process -Wait is avoided on purpose: it also waits for the
    # descendants, i.e. for the server itself.
    $p = Start-Process -FilePath $pgCtl -ArgumentList $Arguments -WindowStyle Hidden -PassThru
    $null = $p.Handle
    $p.WaitForExit()
    return $p.ExitCode
}

function Start-Cluster {
    Initialize-Cluster
    if (-not (Test-Running)) {
        $code = Invoke-PgCtl "-D `"$pgData`" -l `"$logFile`" -w -t 60 start"
        if ($code -ne 0) { throw "pg_ctl start failed ($code); see $logFile" }
    }
    foreach ($db in 'timevault', 'timevault_test') {
        $exists = Invoke-Psql 'postgres' "SELECT 1 FROM pg_database WHERE datname = '$db'"
        if (-not $exists) { Invoke-Psql 'postgres' "CREATE DATABASE $db" | Out-Null }
    }
}

function Stop-Cluster {
    if ((Test-Path (Join-Path $pgData 'PG_VERSION')) -and (Test-Running)) {
        $code = Invoke-PgCtl "-D `"$pgData`" -m fast -w -t 60 stop"
        if ($code -ne 0) { throw "pg_ctl stop failed ($code); see $logFile" }
    }
}

switch ($Action) {
    'init' { Start-Cluster; Write-Host "Private PostgreSQL ready on 127.0.0.1:$Port (the credentials are in $DotEnvFile)." }
    'start' { Start-Cluster; Write-Host "Private PostgreSQL running on 127.0.0.1:$Port." }
    'stop' { Stop-Cluster; Write-Host 'Private PostgreSQL stopped.' }
    'status' {
        if (Test-Running) { Write-Host "running on 127.0.0.1:$Port" } else { Write-Host 'not running'; exit 1 }
    }
    'reset' {
        if (Test-EnvPointsElsewhere) {
            throw "TIMEVAULT_DATABASE_URL in $DotEnvFile points to another PostgreSQL server, which a reset of the private cluster would replace. Remove TIMEVAULT_DATABASE_URL and TIMEVAULT_TEST_DATABASE_URL from .env first if you really want to reset the private cluster."
        }
        Stop-Cluster
        if (Test-Path $dataDir) { Remove-Item -LiteralPath $dataDir -Recurse -Force }
        Start-Cluster
        Write-Host 'Private PostgreSQL recreated.'
    }
}
