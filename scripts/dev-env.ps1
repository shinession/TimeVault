<#
.SYNOPSIS
  Prepares the current PowerShell session for go test and for running the server by hand.

.DESCRIPTION
  Dot-source this script so the changes stay in your session:

      . .\scripts\dev-env.ps1

  <repo>\.env is the only settings file, and the service reads it itself. This script

    - removes the service's TIMEVAULT_* settings from the environment of the session. A variable
      there would override .env, and an older dev-run.ps1 left some behind: the usual reason for a
      service that "ignores" the port written in .env;
    - sets TIMEVAULT_ENV_FILE to <repo>\.env, so a timevault-server that you start by hand, from any
      folder, reads that file;
    - loads what only tests and tools read: TIMEVAULT_TEST_* (for example TIMEVAULT_TEST_DATABASE_URL)
      and the tool locations TIMEVAULT_GO_ROOT and TIMEVAULT_PG_BIN. A tool location that is already set
      in the session is kept;
    - puts <GoRoot>\bin on PATH when go is not found. TIMEVAULT_GO_ROOT must then name the Go
      installation folder (the one that contains bin\go.exe); put it into .env to keep it.

  The settings of the files older versions used (data\local.env, data\pgdev\db.env,
  data\dev\server.env) are moved into .env first; see scripts\dotenv.ps1.
#>
& {
    . (Join-Path $PSScriptRoot 'dotenv.ps1')
    Move-LegacyEnvFiles

    $stale = Remove-SettingsFromEnvironment
    foreach ($name in @(Get-ChildItem Env: | Where-Object { $_.Name -like 'TIMEVAULT_TEST_*' } | ForEach-Object { $_.Name })) {
        Remove-Item -Path ('Env:' + $name)
    }
    foreach ($pair in (Get-DotEnvPairs).GetEnumerator()) {
        $path = 'Env:' + $pair.Key
        if ($pair.Key -like 'TIMEVAULT_TEST_*') { Set-Item -Path $path -Value $pair.Value }
        elseif ($DotEnvToolVariables -contains $pair.Key -and -not (Test-Path -Path $path)) { Set-Item -Path $path -Value $pair.Value }
    }
    if (Test-Path -LiteralPath $DotEnvFile -PathType Leaf) { $env:TIMEVAULT_ENV_FILE = $DotEnvFile }
    Add-GoToPath

    if ($stale.Count -gt 0) {
        Write-Host ('Removed from this session, where they would override .env: ' + ($stale -join ', '))
    }
}
