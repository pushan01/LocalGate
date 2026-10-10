$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $PSScriptRoot
Set-Location $RootDir

if ($env:BUILD_ADMIN -eq "1") {
  & (Join-Path $PSScriptRoot "build-admin.ps1")
}

$Version = $env:VERSION
if ([string]::IsNullOrWhiteSpace($Version)) {
  $Version = "dev"
}

$Output = $env:OUTPUT
if ([string]::IsNullOrWhiteSpace($Output)) {
  $Output = Join-Path $RootDir "bin\localgate.exe"
}

$OutputDir = Split-Path -Parent $Output
if (-not [string]::IsNullOrWhiteSpace($OutputDir)) {
  New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
}

& go build -ldflags "-X main.Version=$Version" -o $Output ./cmd/localgate
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}

Write-Host "built: $Output"
