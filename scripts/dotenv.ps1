<#
.SYNOPSIS
  Helpers for <repo>\.env, the one settings file of the development setup.

.DESCRIPTION
  Dot-source it to use the functions:

      . (Join-Path $PSScriptRoot 'dotenv.ps1')

  Nothing runs and nothing in your session changes until you call a function.

  <repo>\.env is the file the service reads (scripts\dev-run.ps1 points it there) and the file the
  dev-* scripts take their settings from. Older versions kept settings in data\local.env,
  data\pgdev\db.env, data\dev\server.env and data\everything\everything.env; Move-LegacyEnvFiles moves
  them into .env and deletes them.

  The file is read and written as UTF-8 without a BOM, with the syntax the service parses: one
  KEY=VALUE per line, # comments, the value taken literally to the end of the line (one pair of
  matching quotes is removed), the last assignment of a key wins.

  A TIMEVAULT_* environment variable beats the same key in .env, inside the service as well. That is
  why Remove-SettingsFromEnvironment exists: a value left in a terminal by an older script would
  otherwise win over what is written in the file, silently.
#>

$DotEnvFile = Join-Path (Split-Path -Parent $PSScriptRoot) '.env'
$DotEnvPattern = '^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$'

# Where the helpers say what they did. A script whose output stream must stay clean can define its own
# Write-DotEnvInfo after dot-sourcing this file.
function Write-DotEnvInfo([string]$Message) { Write-Host $Message }

# Variables that say where a tool is installed. The service never reads them, so a value in the
# terminal is not a setting that could contradict .env and is left alone, as are TIMEVAULT_TEST_*
# (read by go test only).
$DotEnvToolVariables = @('TIMEVAULT_GO_ROOT', 'TIMEVAULT_PG_BIN', 'TIMEVAULT_EVERYTHING_EXE')

# The comment that goes above a key when a script adds it, so the file explains itself.
$DotEnvNotes = @{
    'TIMEVAULT_DATABASE_URL'        = @('PostgreSQL database for the catalog (history, jobs, the monitored folders).')
    'TIMEVAULT_TEST_DATABASE_URL'   = @('Database for go test. Tests create and drop their own schemas; never point this at the working database.')
    'TIMEVAULT_AUTO_MIGRATE'        = @('true: update the database tables when the service starts.')
    'TIMEVAULT_REPOSITORY_PATH'     = @('Backup folder: where TimeVault stores the contents of the backed-up files (the repository).', 'Keep it outside every monitored folder.')
    'TIMEVAULT_RESTORE_ROOT'        = @('Restore folder: a restore never overwrites the original files. It writes the restored copies into a new folder below this one,', 'and you copy back what you need. Keep it outside every monitored folder and the backup folder. Empty turns restore off.')
    'TIMEVAULT_ADMIN_USER'          = @('Local administrator (optional): user name and password hash. The generated password is in data\dev\admin-password.txt.')
    'TIMEVAULT_SESSION_KEY'         = @('Signs the session cookies (base64 of 32 random bytes). With it, sessions survive a restart.')
    'TIMEVAULT_REDIS_URL'           = @('User directory of the company accounts (the app_user_* hashes in Redis).')
    'TIMEVAULT_LISTEN_ADDR'         = @('Listen address. Other computers need a TLS reverse proxy and TIMEVAULT_ALLOW_NON_LOOPBACK=true as well.')
    'TIMEVAULT_SERVER_HOST'         = @('Name of this server as users type it in network paths (\\server\share\...). With it, every monitored folder needs a', 'display folder on this host, and users never see the real folders of the server.')
    'TIMEVAULT_RESTORE_DISPLAY_ROOT' = @('The restore folder as users reach it over the network (\\server\share\...), so they can pick up restored files.')
    'TIMEVAULT_SSO_HOST'            = @('Single sign-on with the Windows account (optional): the address of the company sign-on service.')
    'TIMEVAULT_BASE_PATH'           = @('Path the site is served under behind a reverse proxy (for example /tv for http://host/tv/). Empty: at the root.')
    'TIMEVAULT_EVERYTHING_URL'      = @('Search: the Everything instance of scripts\dev-everything.ps1 (its reset removes these lines).')
    'TIMEVAULT_TEST_EVERYTHING_URL' = @('Live search tests (go test ./internal/search/...), written by scripts\dev-everything.ps1.')
    'TIMEVAULT_GO_ROOT'             = @('Development scripts only: the folder that contains bin\go.exe.')
    'TIMEVAULT_PG_BIN'              = @('Development scripts only: the PostgreSQL bin folder (pg_ctl.exe, psql.exe).')
}

function ConvertFrom-DotEnvValue([string]$Raw) {
    $v = $Raw.Trim()
    if ($v.Length -ge 2 -and ($v[0] -eq '"' -or $v[0] -eq "'") -and $v[$v.Length - 1] -eq $v[0]) {
        $v = $v.Substring(1, $v.Length - 2)
    }
    return $v
}

# The lines of an env file (the file may be missing), as a list that can be edited.
function Read-DotEnvLines([string]$Path = $DotEnvFile) {
    $list = New-Object 'System.Collections.Generic.List[string]'
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        foreach ($line in ([System.IO.File]::ReadAllText($Path) -split "\r?\n")) { $list.Add($line) }
        if ($list.Count -gt 0 -and $list[$list.Count - 1] -eq '') { $list.RemoveAt($list.Count - 1) }
    }
    return , $list
}

# Writes through a temporary file, so a failure cannot leave a half-written file with the secrets in it.
function Write-DotEnvLines($Lines, [string]$Path = $DotEnvFile) {
    $tmp = $Path + '.tmp'
    [System.IO.File]::WriteAllText($tmp, (($Lines -join "`r`n") + "`r`n"), (New-Object System.Text.UTF8Encoding($false)))
    Move-Item -LiteralPath $tmp -Destination $Path -Force
}

# Key -> value of an env file, in file order. The last assignment of a key wins.
function Get-DotEnvPairs([string]$Path = $DotEnvFile) {
    $pairs = [ordered]@{}
    foreach ($line in (Read-DotEnvLines $Path)) {
        if ($line -match $DotEnvPattern) { $pairs[$Matches[1]] = ConvertFrom-DotEnvValue $Matches[2] }
    }
    return , $pairs
}

# The value of a key in .env, or $null when it is not set there. An empty value is a value.
function Get-DotEnvValue([string]$Key) {
    $pairs = Get-DotEnvPairs
    if ($pairs.Contains($Key)) { return $pairs[$Key] }
    return $null
}

# Creates .env with a commented template when it does not exist. An .env that an earlier version
# of dev-run.ps1 created gets its outdated header replaced, nothing else changes.
function Initialize-DotEnv {
    $header = @(
        '# TimeVault settings: the only settings file of the development setup (git-ignored, it holds secrets).',
        '#',
        '# The service reads it when it starts (scripts\dev-run.ps1 points it here), and so do the dev-*',
        '# scripts and the tests (scripts\dev-env.ps1). One KEY=VALUE per line; the value is taken literally',
        '# to the end of the line. Changes take effect when the service is restarted.',
        '#',
        '# A TIMEVAULT_* environment variable beats the same key here. dev-run.ps1 removes leftovers from',
        '# your terminal, and the service names any that still override this file in its start-up log.'
    )
    $rest = @(
        '#',
        '# Company accounts come from the user directory in Redis (the app_user_* hashes). Without it only',
        '# the local administrator (TIMEVAULT_ADMIN_*) can sign in. Fill in the URL (rediss:// for TLS), then',
        '# check it from the repository folder with:  data\dev\bin\timevault-server.exe check-redis-users',
        '# TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0',
        '#',
        '# Listen address (default 127.0.0.1:8660). Other computers need a TLS reverse proxy and',
        '# TIMEVAULT_ALLOW_NON_LOOPBACK=true as well.',
        '# TIMEVAULT_LISTEN_ADDR=127.0.0.1:8660',
        '#',
        '# Name of this server as users type it in network paths (\\server\share\...). Set it to make the display',
        '# folder of every monitored folder mandatory and checked: users see \\server\share paths, never the real',
        '# folders of the server. The restore folder gets a display path of its own.',
        '# TIMEVAULT_SERVER_HOST=server.example.local',
        '# TIMEVAULT_RESTORE_DISPLAY_ROOT=\\server.example.local\share\restore',
        '#',
        '# Single sign-on with the Windows account (optional): the address of the company sign-on service. Behind a',
        '# reverse proxy also list the proxies in TIMEVAULT_TRUSTED_PROXIES (see configs\timevault.example.env).',
        '# TIMEVAULT_SSO_HOST=sso.example.local',
        '#',
        '# Behind a reverse proxy that serves the site under a path of a shared host name (http://host/tv/), set that',
        '# path (see "Behind a reverse proxy under a path" in docs\runbook.md).',
        '# TIMEVAULT_BASE_PATH=/tv'
    )
    if (-not (Test-Path -LiteralPath $DotEnvFile -PathType Leaf)) {
        Write-DotEnvLines ($header + $rest)
        Write-DotEnvInfo "Created $DotEnvFile, the only settings file."
        return
    }
    $old = @(
        '# TimeVault settings for development. The service reads this file when it starts',
        '# (scripts\dev-run.ps1 points it here). Real environment variables win over it, and dev-run.ps1',
        '# first loads data\pgdev\db.env, data\dev\server.env, data\everything\everything.env and',
        '# data\local.env into the environment, so a setting made in those files cannot be changed here.'
    )
    $lines = Read-DotEnvLines
    if ($lines.Count -ge $old.Count) {
        $same = $true
        for ($i = 0; $i -lt $old.Count; $i++) { if ($lines[$i] -ne $old[$i]) { $same = $false } }
        if ($same) {
            $lines.RemoveRange(0, $old.Count)
            $lines.InsertRange(0, [string[]]$header)
            Write-DotEnvLines $lines
        }
    }
}

# Sets KEY=VALUE in .env: replaces the line of the key, or adds it at the end with its comment.
function Set-DotEnvValue([string]$Key, [string]$Value) {
    if ($Key -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { throw "Not a valid setting name: $Key" }
    if ($Value -match '[\r\n]') { throw "The value of $Key must be a single line." }
    Initialize-DotEnv
    $lines = Read-DotEnvLines
    $last = -1
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match $DotEnvPattern -and $Matches[1] -eq $Key) { $last = $i }
    }
    if ($last -ge 0) {
        $lines[$last] = "$Key=$Value"
    }
    else {
        $note = $DotEnvNotes[$Key]
        if ($note) {
            if ($lines.Count -gt 0 -and $lines[$lines.Count - 1].Trim() -ne '') { $lines.Add('') }
            foreach ($text in $note) { $lines.Add('# ' + $text) }
        }
        $lines.Add("$Key=$Value")
    }
    Write-DotEnvLines $lines
}

# Removes every assignment of the key, and the comment Set-DotEnvValue put above it.
function Remove-DotEnvValue([string]$Key) {
    if (-not (Test-Path -LiteralPath $DotEnvFile -PathType Leaf)) { return }
    $lines = Read-DotEnvLines
    $changed = $false
    for ($i = $lines.Count - 1; $i -ge 0; $i--) {
        if (-not ($lines[$i] -match $DotEnvPattern -and $Matches[1] -eq $Key)) { continue }
        $lines.RemoveAt($i)
        $changed = $true
        $note = @($DotEnvNotes[$Key])
        $start = $i - $note.Count
        if ($note[0] -and $start -ge 0) {
            $same = $true
            for ($j = 0; $j -lt $note.Count; $j++) { if ($lines[$start + $j] -ne ('# ' + $note[$j])) { $same = $false } }
            if ($same) { $lines.RemoveRange($start, $note.Count); $i = $start }
        }
    }
    if ($changed) { Write-DotEnvLines $lines }
}

# Moves the settings of the files older versions used into .env, then deletes those files. A key
# that .env already has keeps the value of .env. A file is deleted only after every one of its keys
# is in .env, and a file with lines that are not KEY=VALUE is left alone.
function Move-LegacyEnvFiles {
    $root = Split-Path -Parent $DotEnvFile
    foreach ($relative in @('data\local.env', 'data\pgdev\db.env', 'data\dev\server.env', 'data\everything\everything.env')) {
        $path = Join-Path $root $relative
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { continue }
        $odd = 0
        foreach ($line in (Read-DotEnvLines $path)) {
            $text = $line.Trim()
            if ($text -ne '' -and -not $text.StartsWith('#') -and $line -notmatch $DotEnvPattern) { $odd++ }
        }
        if ($odd -gt 0) {
            Write-Warning "$relative was left alone: $odd of its lines are not KEY=VALUE. Move what you need into .env by hand, then delete it."
            continue
        }
        $old = Get-DotEnvPairs $path
        $current = Get-DotEnvPairs
        $moved = New-Object 'System.Collections.Generic.List[string]'
        $kept = New-Object 'System.Collections.Generic.List[string]'
        foreach ($key in @($old.Keys)) {
            if (-not $current.Contains($key)) { Set-DotEnvValue $key $old[$key]; $moved.Add($key) }
            elseif ($current[$key] -ne $old[$key]) { $kept.Add($key) }
        }
        $after = Get-DotEnvPairs
        foreach ($key in @($old.Keys)) {
            if (-not $after.Contains($key)) { throw "Could not move $key from $relative into .env, so $relative was not deleted." }
        }
        Remove-Item -LiteralPath $path -Force
        $names = if ($moved.Count -gt 0) { ' (' + ($moved -join ', ') + ')' } else { '' }
        Write-DotEnvInfo "Moved the settings of $relative into .env$names and deleted it."
        if ($kept.Count -gt 0) { Write-DotEnvInfo ('  Already in .env with another value, so the value in .env was kept: ' + ($kept -join ', ')) }
    }
}

# Removes the service's settings from this process' environment and returns their names. They would
# override .env, and an older script, or an earlier session, may have left some behind. Tool
# locations and TIMEVAULT_TEST_* are not settings of the service and stay.
function Remove-SettingsFromEnvironment {
    $names = @(Get-ChildItem Env: | Where-Object {
            $_.Name -like 'TIMEVAULT_*' -and $_.Name -notlike 'TIMEVAULT_TEST_*' -and $DotEnvToolVariables -notcontains $_.Name
        } | ForEach-Object { $_.Name })
    foreach ($name in $names) { Remove-Item -Path ('Env:' + $name) }
    return , $names
}

# Puts <GoRoot>\bin on PATH when go is not found: TIMEVAULT_GO_ROOT from the environment, else from .env.
function Add-GoToPath {
    if (Get-Command go -ErrorAction SilentlyContinue) { return }
    $goRoot = $env:TIMEVAULT_GO_ROOT
    if (-not $goRoot) { $goRoot = Get-DotEnvValue 'TIMEVAULT_GO_ROOT' }
    if ($goRoot) { $env:Path = (Join-Path $goRoot 'bin') + ';' + $env:Path }
}
