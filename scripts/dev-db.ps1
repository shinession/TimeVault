<#
.SYNOPSIS
  Private PostgreSQL instance for TimeVault development and integration tests.

.DESCRIPTION
  Creates and runs a throw-away cluster under <repo>\data\pgdev using the PostgreSQL
  binaries that are already installed. It listens on 127.0.0.1 only, on its own port,
  and never touches an existing PostgreSQL service. The superuser password is random,
  stored only in <repo>\data\pgdev\db.env (git-ignored) and never printed.

  Durability is relaxed (fsync off) on purpose: this cluster is for development only.

  data\pgdev\db.env may be edited to point at another PostgreSQL server (for example the one
  installed on this computer). The scripts then never overwrite or delete that file: creating the
  private cluster and reset stop with an explanation until the file is moved aside.

.PARAMETER Action
  init   Create the cluster and the databases 'timevault' and 'timevault_test'.
  start  Start the cluster (runs init first when needed).
  stop   Stop the cluster.
  status Show whether the cluster accepts connections.
  reset  Stop and delete the cluster, then create a new one.

.PARAMETER PgBin
  Folder with pg_ctl.exe, initdb.exe, psql.exe. Defaults to $env:TIMEVAULT_PG_BIN,
  then pg_ctl on PATH, then the newest C:\Program Files\PostgreSQL\<version>\bin.

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
$envFile = Join-Path $dataDir 'db.env'
$logFile = Join-Path $dataDir 'postgres.log'
$dbUser = 'timevault'

function Resolve-PgBin {
    if ($PgBin) { return $PgBin }
    if ($env:TIMEVAULT_PG_BIN) { return $env:TIMEVAULT_PG_BIN }
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
    $line = Get-Content -LiteralPath $envFile | Where-Object { $_ -like 'TIMEVAULT_DATABASE_URL=*' } | Select-Object -First 1
    if ($line -match '://[^:]+:([^@]+)@') { return $Matches[1] }
    throw "Cannot read the database password from $envFile"
}

# db.env may have been edited to point at another PostgreSQL server, for example the one installed
# on this computer. It is then the developer's file: never overwrite it and never delete it.
function Test-EnvPointsElsewhere {
    if (-not (Test-Path -LiteralPath $envFile)) { return $false }
    $line = Get-Content -LiteralPath $envFile | Where-Object { $_ -like 'TIMEVAULT_DATABASE_URL=*' } | Select-Object -First 1
    if (-not $line) { return $false }
    if ($line -match '@[^/:?#]+:(\d+)') { return ([int]$Matches[1] -ne $Port) }
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
        throw "$envFile points to another PostgreSQL server, and creating the private cluster would overwrite it. Move the file aside first."
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
    $lines = @(
        "TIMEVAULT_DATABASE_URL=$urlBase/timevault?sslmode=disable",
        "TIMEVAULT_TEST_DATABASE_URL=$urlBase/timevault_test?sslmode=disable"
    )
    Set-Content -LiteralPath $envFile -Value $lines -Encoding ascii
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
    'init' { Start-Cluster; Write-Host "Private PostgreSQL ready on 127.0.0.1:$Port (credentials in data\pgdev\db.env)." }
    'start' { Start-Cluster; Write-Host "Private PostgreSQL running on 127.0.0.1:$Port." }
    'stop' { Stop-Cluster; Write-Host 'Private PostgreSQL stopped.' }
    'status' {
        if (Test-Running) { Write-Host "running on 127.0.0.1:$Port" } else { Write-Host 'not running'; exit 1 }
    }
    'reset' {
        if (Test-EnvPointsElsewhere) {
            throw "$envFile points to another PostgreSQL server and would be deleted with the private cluster. Move it aside first if you really want to reset the private cluster."
        }
        Stop-Cluster
        if (Test-Path $dataDir) { Remove-Item -LiteralPath $dataDir -Recurse -Force }
        Start-Cluster
        Write-Host 'Private PostgreSQL recreated.'
    }
}
