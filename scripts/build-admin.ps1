$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $PSScriptRoot
$AdminDir = Join-Path $RootDir "web\admin"
Set-Location $AdminDir

if ($env:SKIP_NPM_CI -ne "1") {
  & npm ci
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}

$Esbuild = Join-Path $AdminDir "node_modules\.bin\esbuild.cmd"
if (-not (Test-Path $Esbuild)) {
  throw "missing frontend dependencies; run npm ci or omit SKIP_NPM_CI=1"
}

& npm run build
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}

Write-Host "built admin assets: $AdminDir\dist"
