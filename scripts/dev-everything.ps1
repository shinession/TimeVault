<#
.SYNOPSIS
    Runs a separate, headless Everything 1.4 instance for TimeVault development and tests.

.DESCRIPTION
    Starts "Everything.exe -instance TimeVault" with its own ini and database under -DataDir, so the
    normal (unnamed) Everything instance is never touched. The instance indexes only -IndexRoot as a
    folder index (no NTFS or ReFS volumes), serves the HTTP JSON API on 127.0.0.1 only with file
    downloads disabled, shows no tray icon and keeps no search or run history.

    HTTP credentials are generated once into <DataDir>\http-credentials.txt. They are never printed
    unless -ShowEnv is given. After a start the settings the server and the live test need are written
    into <repo>\.env, the only settings file (TIMEVAULT_EVERYTHING_* for the server, TIMEVAULT_TEST_EVERYTHING_*
    for the live tests, including the password); restart the server to pick them up.
    The index is rebuilt from scratch on every start, so it always reflects the folder. The generated
    ini layout is the Everything 1.4 one; other versions are refused.

    The instance runs a private copy of Everything.exe in <DataDir>\bin. The installed exe is only read:
    the Everything.ini next to it (for example run_as_admin=1, which would make a standard-user start
    ask for elevation) is never consulted and nothing is written to the installation folder.

.PARAMETER Action
    start    write the ini, start the instance, wait until it answers and has indexed the files, write its settings into .env
    stop     ask the instance to exit; falls back to stopping only its own process by PID
    status   show whether it runs, where it listens and whether it answers
    reset    stop, then delete the instance ini and database and remove its settings from .env (credentials are kept)
    fixture  (re)create the live-test fixture tree <IndexRoot>\source and <IndexRoot>\source2

.PARAMETER EverythingExe
    Path of the installed Everything.exe (1.4 only; it is copied, see above). Default:
    $env:TIMEVAULT_EVERYTHING_EXE, then TIMEVAULT_EVERYTHING_EXE in .env, then the path of a running
    Everything process (a standard-user shell cannot see the path of an elevated one), then
    Everything.exe on PATH, then Program Files.

.PARAMETER Port
    HTTP port on 127.0.0.1. Default 38480.

.PARAMETER IndexRoot
    The only folder that is indexed. Default: <repo>\data\dev\source.

.PARAMETER DataDir
    Folder for the instance ini, database and credentials. Default: <repo>\data\everything.
    It must not be inside IndexRoot.

.PARAMETER IniOverride
    Extra "key=value" ini settings that replace the generated ones (for experiments). Several settings
    can be joined with ';' so they survive "powershell -File".

.PARAMETER ShowEnv
    With start or status: emit PowerShell assignments for the TIMEVAULT_EVERYTHING_* and
    TIMEVAULT_TEST_EVERYTHING_* variables, INCLUDING the password, on the output stream. All other
    messages then go to stderr, so the output can be piped into Invoke-Expression.

.EXAMPLE
    $s = 'powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-everything.ps1'
    Invoke-Expression "$s -Action fixture -IndexRoot data\everything-test"
    Invoke-Expression "$s -Action start -IndexRoot data\everything-test"
    . .\scripts\dev-env.ps1
    go test ./internal/search/... -run Live -count=1 -v
    Invoke-Expression "$s -Action stop"
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('start', 'stop', 'status', 'reset', 'fixture')]
    [string]$Action,

    [string]$EverythingExe,

    [ValidateRange(1, 65535)]
    [int]$Port = 38480,

    [string]$IndexRoot,

    [string]$DataDir,

    [string[]]$IniOverride = @(),

    [switch]$ShowEnv
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$InstanceName = 'TimeVault'
$BindAddress = '127.0.0.1'
$ReadyTimeoutSeconds = 90
$StopTimeoutSeconds = 20
$FixtureMarker = 'tv-fixture-marker.txt'

$RepoRoot = Split-Path -Parent $PSScriptRoot

# With -ShowEnv the output stream carries only environment assignments, so messages go to stderr.
function Write-Info([string]$Message) {
    if ($ShowEnv) { [Console]::Error.WriteLine($Message) } else { Write-Host $Message }
}

. (Join-Path $PSScriptRoot 'dotenv.ps1')
function Write-DotEnvInfo([string]$Message) { Write-Info $Message }
Move-LegacyEnvFiles

function Resolve-FullPath([string]$Path) {
    $full = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    if ($full.Length -gt 3) { $full = $full.TrimEnd('\') }
    return $full
}

if (-not $IndexRoot) { $IndexRoot = Join-Path $RepoRoot 'data\dev\source' }
if (-not $DataDir) { $DataDir = Join-Path $RepoRoot 'data\everything' }
$IndexRoot = Resolve-FullPath $IndexRoot
$DataDir = Resolve-FullPath $DataDir
$IniPath = Join-Path $DataDir "Everything-$InstanceName.ini"
$DbPath = Join-Path $DataDir "Everything-$InstanceName.db"
$CredentialsPath = Join-Path $DataDir 'http-credentials.txt'
$BaseUrl = 'http://{0}:{1}' -f $BindAddress, $Port
$FixtureRoot = Join-Path $IndexRoot 'source'

function Test-IsInside([string]$Child, [string]$Parent) {
    $p = $Parent.TrimEnd('\') + '\'
    return $Child.StartsWith($p, [System.StringComparison]::OrdinalIgnoreCase)
}

function Find-EverythingExe {
    $candidate = $null
    $fromEnvFile = Get-DotEnvValue 'TIMEVAULT_EVERYTHING_EXE'
    if ($EverythingExe) {
        $candidate = $EverythingExe
    } elseif ($env:TIMEVAULT_EVERYTHING_EXE) {
        $candidate = $env:TIMEVAULT_EVERYTHING_EXE
    } elseif ($fromEnvFile) {
        $candidate = $fromEnvFile
    } else {
        # WMI also reports the path of an elevated Everything to a standard-user shell; Get-Process does not.
        $running = @(Get-CimInstance -ClassName Win32_Process -Filter "Name = 'Everything.exe'" | Where-Object { $_.ExecutablePath }) | Select-Object -First 1
        if ($running) {
            $candidate = $running.ExecutablePath
        } else {
            $command = Get-Command Everything.exe -ErrorAction SilentlyContinue
            if ($command) { $candidate = $command.Source }
        }
    }
    if (-not $candidate) {
        foreach ($dir in $env:ProgramFiles, ${env:ProgramFiles(x86)}) {
            if ($dir -and (Test-Path -LiteralPath (Join-Path $dir 'Everything\Everything.exe'))) {
                $candidate = Join-Path $dir 'Everything\Everything.exe'
                break
            }
        }
    }
    if (-not $candidate -or -not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        throw 'Everything.exe not found (a standard-user shell cannot see the path of an elevated Everything). Pass -EverythingExe or set TIMEVAULT_EVERYTHING_EXE.'
    }
    $candidate = (Resolve-Path -LiteralPath $candidate).ProviderPath
    $version = (Get-Item -LiteralPath $candidate).VersionInfo
    if ($version.FileMajorPart -ne 1 -or $version.FileMinorPart -ne 4) {
        throw "Unsupported Everything version $($version.FileVersion): this script generates the 1.4 ini layout."
    }
    return $candidate
}

function Get-InstanceProcess {
    $all = @(Get-CimInstance -ClassName Win32_Process -Filter "Name = 'Everything.exe'")
    return @($all | Where-Object { $_.CommandLine -and $_.CommandLine -match "(?i)[-/]instance\s+`"?$InstanceName`"?(\s|$)" })
}

# Plain .NET on purpose: after Get-NetTCPConnection (a CDXML cmdlet) in the same PowerShell process, an Everything
# 1.4.1.1024 launched from it crashes during startup when the shell is not elevated.
function Get-TcpListener([int]$LocalPort) {
    return @([System.Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners() | Where-Object { $_.Port -eq $LocalPort })
}

function New-Token([int]$Bytes) {
    $buffer = New-Object byte[] $Bytes
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($buffer) } finally { $rng.Dispose() }
    return (($buffer | ForEach-Object { $_.ToString('x2') }) -join '')
}

function Get-Credentials {
    if (-not (Test-Path -LiteralPath $CredentialsPath)) {
        New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
        $lines = @("username=tv-$(New-Token 4)", "password=$(New-Token 32)")
        [System.IO.File]::WriteAllText($CredentialsPath, ($lines -join "`r`n") + "`r`n", (New-Object System.Text.UTF8Encoding($false)))
    }
    $map = @{}
    foreach ($line in Get-Content -LiteralPath $CredentialsPath) {
        $pair = $line -split '=', 2
        if ($pair.Count -eq 2) { $map[$pair[0].Trim()] = $pair[1].Trim() }
    }
    if (-not $map.ContainsKey('username') -or -not $map.ContainsKey('password')) {
        throw 'http-credentials.txt is malformed; delete it to generate new credentials.'
    }
    return [pscustomobject]@{ Username = $map['username']; Password = $map['password'] }
}

function ConvertTo-IniListItem([string]$Value) {
    if ($Value -match '[,"]') {
        return '"' + $Value.Replace('\', '\\').Replace('"', '\"') + '"'
    }
    return $Value
}

function New-IniText($Credentials) {
    # Verified on Everything 1.4.1.1024: the four auto_include_* switches keep the instance off every NTFS/ReFS
    # volume and folders + http_server_* make IndexRoot searchable. Per-folder monitoring, the size and date
    # columns and the other index settings work with their defaults. The rest only switches off defaults that
    # would show a tray icon, keep a run history, check for updates or allow file downloads.
    $settings = [ordered]@{
        run_as_admin                        = '0'
        show_tray_icon                      = '0'
        check_for_updates_on_startup        = '0'
        search_history_enabled              = '0'
        run_history_enabled                 = '0'
        auto_include_fixed_volumes          = '0'
        auto_include_removable_volumes      = '0'
        auto_include_fixed_refs_volumes     = '0'
        auto_include_removable_refs_volumes = '0'
        folders                             = (ConvertTo-IniListItem $IndexRoot)
        etp_server_enabled                  = '0'
        http_server_enabled                 = '1'
        http_server_bindings                = $BindAddress
        http_server_port                    = [string]$Port
        http_server_username                = $Credentials.Username
        http_server_password                = $Credentials.Password
        http_server_allow_file_download     = '0'
        http_server_logging_enabled         = '0'
    }
    foreach ($override in ($IniOverride | ForEach-Object { $_ -split ';' })) {
        if (-not $override.Trim()) { continue }
        if ($override -notmatch '^\s*([A-Za-z0-9_]+)\s*=(.*)$') { throw "Invalid -IniOverride '$override', expected key=value." }
        $settings[$Matches[1]] = $Matches[2].Trim()
    }
    $lines = @('; TimeVault development instance. Generated by scripts\dev-everything.ps1; edits are overwritten on start.', '[Everything]')
    foreach ($key in $settings.Keys) { $lines += '{0}={1}' -f $key, $settings[$key] }
    return ($lines -join "`r`n") + "`r`n"
}

function Invoke-Everything([string]$Query, $Credentials) {
    $request = [System.Net.HttpWebRequest]::Create("$BaseUrl/?$Query")
    $request.Method = 'GET'
    $request.Timeout = 5000
    $request.ReadWriteTimeout = 5000
    $request.AllowAutoRedirect = $false
    $request.Proxy = $null
    $pair = '{0}:{1}' -f $Credentials.Username, $Credentials.Password
    $request.Headers.Add('Authorization', 'Basic ' + [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($pair)))
    try {
        $response = $request.GetResponse()
    } catch [System.Net.WebException] {
        $response = $_.Exception.Response
        if (-not $response) {
            return [pscustomobject]@{ Status = 0; Json = $null; Error = $_.Exception.Status.ToString() }
        }
    }
    try {
        $reader = New-Object System.IO.StreamReader($response.GetResponseStream(), [System.Text.Encoding]::UTF8)
        $body = $reader.ReadToEnd()
        $status = [int]$response.StatusCode
    } finally {
        $response.Close()
    }
    $json = $null
    if ($status -eq 200) {
        try { $json = $body | ConvertFrom-Json } catch { $json = $null }
    }
    return [pscustomobject]@{ Status = $status; Json = $json; Error = $null }
}

function Get-TotalResults($Response) {
    if ($Response.Json -and $Response.Json.PSObject.Properties['totalResults']) {
        return [int]$Response.Json.totalResults
    }
    return -1
}

function Get-ReadinessProbe {
    if (Test-Path -LiteralPath (Join-Path $FixtureRoot $FixtureMarker)) { return $FixtureMarker }
    if (-not (Test-Path -LiteralPath $IndexRoot -PathType Container)) { return $null }
    $first = Get-ChildItem -LiteralPath $IndexRoot -Recurse -Force -File -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($first) { return $first.Name }
    return $null
}

function Wait-Ready($Credentials, $Launched) {
    $probe = Get-ReadinessProbe
    $search = if ($probe) { '"' + $probe + '"' } else { '*' }
    $query = 'json=1&count=0&search=' + [uri]::EscapeDataString($search)
    $deadline = (Get-Date).AddSeconds($ReadyTimeoutSeconds)
    $started = Get-Date
    $gone = $null
    while ((Get-Date) -lt $deadline) {
        # The launched process can hand over to another one, so only a vanished instance that stays vanished is a failure.
        if ($Launched.HasExited -and @(Get-InstanceProcess).Count -eq 0) {
            if (-not $gone) { $gone = Get-Date }
            if (((Get-Date) - $gone).TotalSeconds -gt 5) {
                throw "The Everything instance exited during startup (exit code $($Launched.ExitCode))."
            }
        } else {
            $gone = $null
        }
        $response = Invoke-Everything $query $Credentials
        $total = Get-TotalResults $response
        if ($response.Status -eq 200 -and ($total -ge 1 -or -not $probe)) {
            return ((Get-Date) - $started).TotalSeconds
        }
        if ($response.Status -eq 401) { throw 'The instance rejected the generated credentials (HTTP 401).' }
        Start-Sleep -Milliseconds 500
    }
    throw "Timed out after $ReadyTimeoutSeconds s waiting for the instance to answer and index the files."
}

function Get-PrivateExe([string]$SourceExe) {
    $binDir = Join-Path $DataDir 'bin'
    $target = Join-Path $binDir 'Everything.exe'
    if ($SourceExe -ieq $target) { return $target }
    $stale = $true
    if (Test-Path -LiteralPath $target -PathType Leaf) {
        $from = Get-Item -LiteralPath $SourceExe
        $to = Get-Item -LiteralPath $target
        $stale = ($from.Length -ne $to.Length) -or ($from.LastWriteTimeUtc -ne $to.LastWriteTimeUtc)
    }
    if ($stale) {
        New-Item -ItemType Directory -Force -Path $binDir | Out-Null
        Copy-Item -LiteralPath $SourceExe -Destination $target -Force
    }
    return $target
}

function Start-Instance {
    $source = Find-EverythingExe
    $running = @(Get-InstanceProcess)
    if ($running.Count -gt 0) {
        Write-EnvSettings
        Write-Info "Instance '$InstanceName' is already running (pid $($running[0].ProcessId)). Use -Action reset to rebuild it."
        return
    }
    if (-not (Test-Path -LiteralPath $IndexRoot -PathType Container)) {
        New-Item -ItemType Directory -Force -Path $IndexRoot | Out-Null
    }
    if ((Test-IsInside $DataDir $IndexRoot) -or (Test-IsInside $IndexRoot $DataDir) -or ($DataDir -ieq $IndexRoot)) {
        throw 'DataDir and IndexRoot must not contain each other.'
    }
    $busy = @(Get-TcpListener $Port)
    if ($busy.Count -gt 0) { throw "TCP port $Port is already in use." }

    New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
    $exe = Get-PrivateExe $source
    $credentials = Get-Credentials
    Remove-Item -LiteralPath $DbPath -Force -ErrorAction SilentlyContinue
    [System.IO.File]::WriteAllText($IniPath, (New-IniText $credentials), (New-Object System.Text.UTF8Encoding($false)))

    $arguments = '-instance {0} -config "{1}" -db "{2}" -startup' -f $InstanceName, $IniPath, $DbPath
    $launched = Start-Process -FilePath $exe -ArgumentList $arguments -WindowStyle Hidden -PassThru
    $seconds = Wait-Ready $credentials $launched
    Write-EnvSettings
    $pids = (@(Get-InstanceProcess) | ForEach-Object { $_.ProcessId }) -join ','
    Write-Info ('Instance ''{0}'' is ready after {1:N1} s (pid {2}).' -f $InstanceName, $seconds, $pids)
}

function Stop-Instance {
    $processes = @(Get-InstanceProcess)
    if ($processes.Count -eq 0) {
        Write-Info "Instance '$InstanceName' is not running."
        return
    }
    $exe = $processes[0].ExecutablePath
    if ($exe) {
        Start-Process -FilePath $exe -ArgumentList ('-instance {0} -exit' -f $InstanceName) -WindowStyle Hidden | Out-Null
    }
    $deadline = (Get-Date).AddSeconds($StopTimeoutSeconds)
    while ((Get-Date) -lt $deadline -and @(Get-InstanceProcess).Count -gt 0) { Start-Sleep -Milliseconds 250 }
    $left = @(Get-InstanceProcess)
    foreach ($process in $left) {
        Write-Info "WARNING: instance did not exit in time; stopping pid $($process.ProcessId)."
        Stop-Process -Id $process.ProcessId -Force
    }
    Write-Info "Instance '$InstanceName' stopped."
}

function Remove-InstanceFiles {
    foreach ($file in Get-ChildItem -LiteralPath $DataDir -Filter "Everything-$InstanceName*" -File -ErrorAction SilentlyContinue) {
        Remove-Item -LiteralPath $file.FullName -Force
    }
    foreach ($key in $SettingKeys) { Remove-DotEnvValue $key }
}

# The settings of this instance in .env, the only settings file. Like http-credentials.txt they hold the
# password; .env is git-ignored. The test variables are only set once the fixture exists.
$SettingKeys = @(
    'TIMEVAULT_EVERYTHING_URL', 'TIMEVAULT_EVERYTHING_USERNAME', 'TIMEVAULT_EVERYTHING_PASSWORD',
    'TIMEVAULT_TEST_EVERYTHING_URL', 'TIMEVAULT_TEST_EVERYTHING_USERNAME', 'TIMEVAULT_TEST_EVERYTHING_PASSWORD', 'TIMEVAULT_TEST_EVERYTHING_ROOT'
)

function Write-EnvSettings {
    $credentials = Get-Credentials
    Set-DotEnvValue 'TIMEVAULT_EVERYTHING_URL' $BaseUrl
    Set-DotEnvValue 'TIMEVAULT_EVERYTHING_USERNAME' $credentials.Username
    Set-DotEnvValue 'TIMEVAULT_EVERYTHING_PASSWORD' $credentials.Password
    if (Test-Path -LiteralPath (Join-Path $FixtureRoot $FixtureMarker)) {
        Set-DotEnvValue 'TIMEVAULT_TEST_EVERYTHING_URL' $BaseUrl
        Set-DotEnvValue 'TIMEVAULT_TEST_EVERYTHING_USERNAME' $credentials.Username
        Set-DotEnvValue 'TIMEVAULT_TEST_EVERYTHING_PASSWORD' $credentials.Password
        Set-DotEnvValue 'TIMEVAULT_TEST_EVERYTHING_ROOT' $FixtureRoot
    }
}

function Show-Status {
    $processes = @(Get-InstanceProcess)
    Write-Info ('instance  : {0}' -f $InstanceName)
    if ($processes.Count -eq 0) {
        Write-Info 'running   : no'
        return
    }
    Write-Info ('running   : yes (pid {0})' -f (($processes | ForEach-Object { $_.ProcessId }) -join ','))
    $listeners = @(Get-TcpListener $Port)
    if ($listeners.Count -gt 0) {
        $addresses = ($listeners | ForEach-Object { '{0}:{1}' -f $_.Address, $_.Port }) -join ', '
        Write-Info ('listening : {0}' -f $addresses)
    } else {
        Write-Info ('listening : nothing on port {0}' -f $Port)
    }
    $response = Invoke-Everything 'json=1&count=0&search=%2A' (Get-Credentials)
    if ($response.Status -eq 200) {
        Write-Info ('health    : ok, {0} indexed items' -f (Get-TotalResults $response))
    } elseif ($response.Status -gt 0) {
        Write-Info ('health    : degraded, HTTP {0}' -f $response.Status)
    } else {
        Write-Info ('health    : unreachable ({0})' -f $response.Error)
    }
}

function Write-Env {
    if ($ShowEnv) {
        $credentials = Get-Credentials
        $values = [ordered]@{
            TIMEVAULT_EVERYTHING_URL           = $BaseUrl
            TIMEVAULT_EVERYTHING_USERNAME      = $credentials.Username
            TIMEVAULT_EVERYTHING_PASSWORD      = $credentials.Password
            TIMEVAULT_TEST_EVERYTHING_URL      = $BaseUrl
            TIMEVAULT_TEST_EVERYTHING_USERNAME = $credentials.Username
            TIMEVAULT_TEST_EVERYTHING_PASSWORD = $credentials.Password
            TIMEVAULT_TEST_EVERYTHING_ROOT     = $FixtureRoot
        }
        foreach ($name in $values.Keys) {
            '$env:{0} = ''{1}''' -f $name, ([string]$values[$name]).Replace("'", "''")
        }
        return
    }
    Write-Info ('url       : {0}' -f $BaseUrl)
    Write-Info ('index root: {0}' -f $IndexRoot)
    Write-Info ('test root : {0} (TIMEVAULT_TEST_EVERYTHING_ROOT)' -f $FixtureRoot)
    Write-Info ('settings  : written into {0}, the only settings file (restart the server to pick them up)' -f $DotEnvFile)
    Write-Info ('credentials are stored in {0}; add -ShowEnv to emit them as environment assignments.' -f $CredentialsPath)
}

# Live-test fixture. internal\search\everything_live_test.go documents and asserts exactly this tree.
# Names are built from code points so this script stays plain ASCII.
function New-Fixture {
    $report = [regex]::Unescape('\u62a5\u544a 2024 v2.docx')
    $unicodeDir = [regex]::Unescape('\u6587\u6863 caf\u00e9')
    $cafe = [regex]::Unescape('caf\u00e9 men\u00fc.txt')
    $emoji = 'emoji ' + [char]::ConvertFromUtf32(0x1F600) + ' needle.txt'
    $fixedTime = New-Object DateTime 2024, 3, 5, 6, 7, 8, ([System.DateTimeKind]::Utc)
    $utf8 = New-Object System.Text.UTF8Encoding($false)

    foreach ($name in 'source', 'source2') {
        $dir = [System.IO.Path]::Combine($IndexRoot, $name)
        if ([System.IO.Directory]::Exists($dir)) {
            if (-not [System.IO.File]::Exists([System.IO.Path]::Combine($dir, $FixtureMarker))) {
                throw "Refusing to replace '$dir': it exists and is not a TimeVault fixture."
            }
            [System.IO.Directory]::Delete($dir, $true)
        }
        [void][System.IO.Directory]::CreateDirectory($dir)
    }

    function Add-File([string]$Dir, [string]$Name, [string]$Content) {
        $folder = [System.IO.Path]::Combine($IndexRoot, $Dir)
        [void][System.IO.Directory]::CreateDirectory($folder)
        [System.IO.File]::WriteAllText([System.IO.Path]::Combine($folder, $Name), $Content, $utf8)
    }

    Add-File 'source' $FixtureMarker 'marker'
    Add-File 'source' 'readme.txt' 'hello world'
    [System.IO.File]::SetLastWriteTimeUtc((Join-Path $FixtureRoot 'readme.txt'), $fixedTime)
    Add-File 'source' 'needle top.txt' 'needle'
    Add-File 'source\docs' 'report 2024 v1.docx' 'v1'
    Add-File 'source\docs' $report 'v2'
    Add-File 'source\docs' $cafe 'cafe'
    Add-File 'source\docs' 'a_b.txt' 'ab'
    Add-File 'source\docs' 'a_c.txt' 'ac'
    Add-File 'source\docs' 'has!x.txt' 'bang'
    Add-File 'source\docs' 'x plain.txt' 'x'
    Add-File 'source\docs' 'size gt1.txt' 'hello'
    Add-File 'source\docs' $emoji 'emoji'
    Add-File 'source\docs\deep\er\est' 'buried needle.txt' 'deep'
    Add-File ('source\' + $unicodeDir) 'inner needle.txt' 'inner'
    Add-File 'source\sub dir with spaces' 'spaced needle.txt' 'spaced'
    Add-File 'source\folder needle' 'inside.txt' 'inside'
    Add-File 'source\odd !;,''^&()[]{}$#@+=~ dir' 'odd needle.txt' 'odd'
    Add-File 'source\pct %TEMP% dir' 'pct needle.txt' 'pct'
    1..130 | ForEach-Object { Add-File 'source\bulk' ('bulk-{0:D3}.dat' -f $_) ('{0}' -f $_) }

    Add-File 'source2' $FixtureMarker 'marker'
    Add-File 'source2' 'outside needle.txt' 'outside'
    Add-File 'source2' $report 'v2'
    Add-File 'source2' 'has!x.txt' 'bang'
    1..5 | ForEach-Object { Add-File 'source2\bulk' ('bulk-{0:D3}.dat' -f $_) ('{0}' -f $_) }

    $count = @([System.IO.Directory]::EnumerateFiles($IndexRoot, '*', [System.IO.SearchOption]::AllDirectories)).Count
    Write-Info ("Fixture created under '{0}' ({1} files)." -f $IndexRoot, $count)
}

try {
    switch ($Action) {
        'start' {
            Start-Instance
            Show-Status
            Write-Env
        }
        'stop' { Stop-Instance }
        'status' {
            Show-Status
            Write-Env
        }
        'reset' {
            Stop-Instance
            Remove-InstanceFiles
            Write-Info "Removed the instance ini and database from '$DataDir'."
        }
        'fixture' { New-Fixture }
    }
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
