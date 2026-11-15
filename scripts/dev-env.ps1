<#
.SYNOPSIS
  Loads the git-ignored development settings into the current PowerShell session.

.DESCRIPTION
  Reads KEY=VALUE lines from the env files that the dev-* scripts create under <repo>\data
  and sets them as environment variables. Dot-source it so the variables stay in your session:

      . .\scripts\dev-env.ps1

  If the Go toolchain is not on PATH, set TIMEVAULT_GO_ROOT to the Go installation folder
  (for example the folder that contains bin\go.exe) before dot-sourcing and it is added to PATH.
#>
$repoRoot = Split-Path -Parent $PSScriptRoot
$envFiles = @(
    (Join-Path $repoRoot 'data\pgdev\db.env'),
    (Join-Path $repoRoot 'data\dev\server.env'),
    (Join-Path $repoRoot 'data\everything\everything.env')
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
