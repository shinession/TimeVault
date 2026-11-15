<#
.SYNOPSIS
  Loads the git-ignored development settings into the current PowerShell session.

.DESCRIPTION
  Reads KEY=VALUE lines from the env files under <repo>\data and sets them as environment
  variables. The dev-* scripts create most of the files; data\local.env is yours to create.
  Dot-source this script so the variables stay in your session:

      . .\scripts\dev-env.ps1

  If the Go toolchain is not on PATH, TIMEVAULT_GO_ROOT must name the Go installation folder (the
  one that contains bin\go.exe); <GoRoot>\bin is then added to PATH for the session. To keep that
  across terminals without changing the system PATH, put this line into data\local.env (git-ignored,
  loaded last, meant for settings that belong to one machine):

      TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>
#>
$repoRoot = Split-Path -Parent $PSScriptRoot
$envFiles = @(
    (Join-Path $repoRoot 'data\pgdev\db.env'),
    (Join-Path $repoRoot 'data\dev\server.env'),
    (Join-Path $repoRoot 'data\everything\everything.env'),
    (Join-Path $repoRoot 'data\local.env')
)

foreach ($file in $envFiles) {
    if (-not (Test-Path -LiteralPath $file)) { continue }
    foreach ($line in Get-Content -LiteralPath $file) {
        if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$') {
            Set-Item -Path ('Env:' + $Matches[1]) -Value $Matches[2]
        }
    }
}

if ($env:TIMEVAULT_GO_ROOT -and -not (Get-Command go -ErrorAction SilentlyContinue)) {
    $env:Path = (Join-Path $env:TIMEVAULT_GO_ROOT 'bin') + ';' + $env:Path
}
