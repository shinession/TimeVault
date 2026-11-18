<#
.SYNOPSIS
  Builds a release: the program and the manifest a running service reads to find out that a new version is out.

.DESCRIPTION
  Writes two files into the release folder (default: <repo>\release):

      timevault.exe    the program
      timevault.json   the manifest: latest_version, sha256, package_url, size

  A service with TIMEVAULT_UPDATE_URL set reads timevault.json from time to time. When latest_version is not
  the version it runs, it downloads the program, checks the size and the sha256, waits until no job runs, puts
  the new program in place of the old one and starts it (see "Automatic updates" in docs\runbook.md).

  The script
  1. runs go vet (unless -SkipChecks);
  2. builds the program into a temporary name, so a failed build leaves the folder as it was;
  3. asks the program for its version (the number in cmd\timevault-server\main.go), measures it and hashes it;
  4. puts the program in place and writes the manifest LAST, atomically, as UTF-8 without a byte order mark.
  The manifest is the last thing to change, so a service that reads it never sees a version whose program is not
  there yet. (A manual copy to a server is not atomic: a service that reads the folder while it is copied finds a
  size or a hash that does not match and tries again later.)

  The version is the one written in the source. A release with the version of the one before it is not an update
  to any service: raise "version" in cmd\timevault-server\main.go first. The script warns when it sees that.

  The Go toolchain must be on PATH, or TIMEVAULT_GO_ROOT (in .env) must name the Go installation folder.

.PARAMETER OutDir
  The release folder. Default: <repo>\release.

.PARAMETER ExeName
  The file name of the program in the release. Default: timevault.exe.

.PARAMETER BaseUrl
  When the services read the release over http(s), the address of the release folder, for example
  https://tools.example.local/tv-release/ . package_url then holds the full address of the program. Without it
  package_url is the file name, which a service resolves next to the manifest it read: right for a folder or a
  share, and for http(s) when the program is served from the same folder as the manifest.

.PARAMETER SkipChecks
  Do not run go vet before the build.
#>
[CmdletBinding()]
param(
    [string]$OutDir,
    [string]$ExeName = 'timevault.exe',
    [string]$BaseUrl = '',
    [switch]$SkipChecks
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'dotenv.ps1')

if (-not $OutDir) { $OutDir = Join-Path $repoRoot 'release' }
$OutDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutDir)

if ($ExeName -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*\.exe$') {
    throw "ExeName must be a plain file name that ends in .exe (letters, digits, . _ -), not '$ExeName'."
}
$packageUrl = $ExeName
if ($BaseUrl) {
    $uri = $null
    if (-not [System.Uri]::TryCreate($BaseUrl, [System.UriKind]::Absolute, [ref]$uri) -or ($uri.Scheme -ne 'http' -and $uri.Scheme -ne 'https')) {
        throw "BaseUrl must be an http(s) address, for example https://tools.example.local/tv-release/ , not '$BaseUrl'."
    }
    if ($uri.UserInfo -or $uri.Query -or $uri.Fragment) { throw 'BaseUrl must not contain a user name, a password, a query or a fragment.' }
    $packageUrl = $BaseUrl.TrimEnd('/') + '/' + $ExeName
}

Add-GoToPath
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'The go command was not found. Add Go to PATH, or put the line TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe> into .env.'
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
# The temporary program keeps the .exe extension: PowerShell will not run a file that does not have one.
$building = Join-Path $OutDir ([System.IO.Path]::GetFileNameWithoutExtension($ExeName) + '.building.exe')
$final = Join-Path $OutDir $ExeName
$manifestFile = Join-Path $OutDir 'timevault.json'
$manifestTemp = $manifestFile + '.tmp'

# The version of the release that is in the folder now, to warn about a release that is no update.
$previous = $null
if (Test-Path -LiteralPath $manifestFile -PathType Leaf) {
    try { $previous = Get-Content -LiteralPath $manifestFile -Raw | ConvertFrom-Json } catch { $previous = $null }
}

Push-Location $repoRoot
try {
    if (-not $SkipChecks) {
        Write-Host 'go vet ./... '
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'go vet found problems; fix them, or build anyway with -SkipChecks.' }
    }
    Write-Host "Building $final"
    go build -trimpath -ldflags '-s -w' -o $building ./cmd/timevault-server
    if ($LASTEXITCODE -ne 0) { throw 'go build failed.' }

    $said = (& $building version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $said -notmatch '^timevault-server (\S+)$') { throw "The new program does not say what version it is (it printed: $said)." }
    $version = $Matches[1] -replace '^[vV](?=\d)', ''
    if ($version -notmatch '^[0-9][0-9A-Za-z._+-]{0,31}$') { throw "'$version' is not a version number a service accepts." }

    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $building).Hash.ToLowerInvariant()
    $size = (Get-Item -LiteralPath $building).Length

    if ($previous -and $previous.latest_version -eq $version -and $previous.sha256 -ne $hash) {
        Write-Warning "The release in $OutDir has the version $version as well, but another program. Services that run $version will not update. Raise 'version' in cmd\timevault-server\main.go to release a new one."
    }

    Move-Item -LiteralPath $building -Destination $final -Force

    $manifest = [ordered]@{ latest_version = $version; sha256 = $hash; package_url = $packageUrl; size = [int64]$size }
    $json = ($manifest | ConvertTo-Json) + "`n"
    [System.IO.File]::WriteAllText($manifestTemp, $json, (New-Object System.Text.UTF8Encoding($false)))
    Move-Item -LiteralPath $manifestTemp -Destination $manifestFile -Force
}
finally {
    Pop-Location
    Remove-Item -LiteralPath $building, $manifestTemp -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "Release $version"
Write-Host ("  program   {0}  ({1:N0} bytes)" -f $final, $size)
Write-Host "  sha256    $hash"
Write-Host "  manifest  $manifestFile"
Write-Host "  address   $packageUrl"
Write-Host ''
Write-Host 'Copy both files to the release folder the services read (TIMEVAULT_UPDATE_URL), the manifest last.'
Write-Host 'A service that runs another version downloads the program, waits for its running jobs, and restarts itself.'
