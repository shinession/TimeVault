<#
.SYNOPSIS
  Fills the development source folder with sample files and creates several recovery points
  through the JSON API, so the UI has real history to show.

.DESCRIPTION
  Requires the server from scripts\dev-run.ps1 to be running. It signs in as the development
  admin, registers <repo>\data\dev\source as the source "Demo files" (if needed), takes a first
  backup, then modifies, deletes and adds files and takes two more. The last changes are left
  on disk without a backup so the Explorer shows "changed", "new" and "deleted" items.

.PARAMETER BaseUrl
  Address of the running server. Without it the address is built from TIMEVAULT_LISTEN_ADDR in .env
  (default http://127.0.0.1:8660). After dev-run.ps1 -Port, pass it, for example -BaseUrl http://127.0.0.1:9000.
#>
[CmdletBinding()]
param([string]$BaseUrl)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'dotenv.ps1')
if (-not $BaseUrl) {
    $listen = Get-DotEnvValue 'TIMEVAULT_LISTEN_ADDR'
    if (-not $listen) { $listen = '127.0.0.1:8660' }
    $BaseUrl = 'http://' + ($listen -replace '^(0\.0\.0\.0|\[::\]|::)(?=:)', '127.0.0.1')
}
$devDir = Join-Path $repoRoot 'data\dev'
$source = Join-Path $devDir 'source'
$passwordFile = Join-Path $devDir 'admin-password.txt'
if (-not (Test-Path $passwordFile)) { throw "Run scripts\dev-run.ps1 first ($passwordFile is missing)." }
$password = (Get-Content -LiteralPath $passwordFile -Raw).Trim()

# Chinese folder and file names built from code points keep this script ASCII only.
$reportsZh = ([char]0x62A5) + ([char]0x544A)
$summaryZh = ([char]0x5E74) + ([char]0x5EA6) + ([char]0x603B) + ([char]0x7ED3)

function Write-Demo([string]$Rel, [string]$Text) {
    $path = Join-Path $source $Rel
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
    Set-Content -LiteralPath $path -Value $Text -Encoding utf8
}

# Sign in. The cookie jar keeps the session; the CSRF token goes into a header for every POST.
$session = $null
try {
    Invoke-WebRequest -Uri "$BaseUrl/login" -Method Post -Body @{ username = 'admin'; password = $password; next = '/' } `
        -SessionVariable session -MaximumRedirection 0 -UseBasicParsing | Out-Null
}
catch { }
$info = Invoke-RestMethod -Uri "$BaseUrl/api/v1/session" -WebSession $session
$headers = @{ 'X-CSRF-Token' = $info.csrf_token }

function Invoke-Api([string]$Method, [string]$Path, $Body) {
    $req = @{ Uri = "$BaseUrl$Path"; Method = $Method; WebSession = $session; Headers = $headers }
    if ($null -ne $Body) { $req.Body = ($Body | ConvertTo-Json -Depth 6); $req.ContentType = 'application/json' }
    Invoke-RestMethod @req
}

function Invoke-Backup([int]$SourceId) {
    $started = Invoke-Api Post '/api/v1/backups' @{ source_id = $SourceId; rehash = $false }
    $id = $started.job.id
    do {
        Start-Sleep -Milliseconds 700
        $job = (Invoke-Api Get "/api/v1/jobs/$id" $null).job
    } while ($job.active)
    if ($job.state -ne 'succeeded') { throw "Backup job $id ended as $($job.state): $($job.error)" }
    Write-Host "  backup job $id succeeded"
}

Write-Host 'Creating the first set of files...'
Write-Demo 'readme.txt' "TimeVault demo folder`r`nEdit, delete or add files, then back up again."
Write-Demo 'Projects\Alpha\spec.md' "# Alpha spec`r`nVersion 1: the first draft."
Write-Demo 'Projects\Alpha\budget.csv' "item,cost`r`nlicenses,1200`r`nhardware,5400"
Write-Demo 'Projects\Beta\plan.txt' 'Beta plan, draft one.'
Write-Demo 'Reports\2026-Q1.txt' 'Q1 results: steady growth.'
Write-Demo 'Reports\2026-Q2.txt' 'Q2 results: new customers.'
Write-Demo 'Notes\todo.txt' "- buy milk`r`n- call the bank"
Write-Demo 'Photos\2026\trip\img-001.txt' 'pretend this is a photo'
Write-Demo 'Photos\2026\trip\img-002.txt' 'another pretend photo'
Write-Demo "$reportsZh\$summaryZh.txt" 'Annual summary, first version.'

$sources = (Invoke-Api Get '/api/v1/sources' $null).sources
$demo = $sources | Where-Object { $_.root_path -ieq $source } | Select-Object -First 1
if (-not $demo) {
    $demo = Invoke-Api Post '/api/v1/sources' @{ name = 'Demo files'; root_path = $source }
    Write-Host "Registered source '$($demo.name)'."
}

Write-Host 'Backup 1 (full)...'
Invoke-Backup $demo.id

Write-Host 'Changing files...'
Write-Demo 'Projects\Alpha\spec.md' "# Alpha spec`r`nVersion 2: added the security section."
Remove-Item -LiteralPath (Join-Path $source 'Notes\todo.txt') -Force
Write-Demo 'Reports\2026-Q3.txt' 'Q3 results: record quarter.'
Write-Host 'Backup 2...'
Invoke-Backup $demo.id

Write-Host 'Changing files again...'
Remove-Item -LiteralPath (Join-Path $source 'Photos') -Recurse -Force
Write-Demo 'Projects\Alpha\budget.csv' "item,cost`r`nlicenses,1500`r`nhardware,5400`r`ntraining,800"
Write-Demo "$reportsZh\$summaryZh.txt" 'Annual summary, second version with the final numbers.'
Write-Host 'Backup 3...'
Invoke-Backup $demo.id

Write-Host 'Leaving uncommitted changes for the Explorer to show...'
Write-Demo 'Projects\Beta\plan.txt' 'Beta plan, edited after the last backup.'
Write-Demo 'Notes\ideas.txt' 'A brand new file that has not been backed up yet.'
Remove-Item -LiteralPath (Join-Path $source 'Reports\2026-Q1.txt') -Force

Write-Host "Done. Open $BaseUrl/ (anyone can browse; sign in as admin to back up, restore or change settings)."
