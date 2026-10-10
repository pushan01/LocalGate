$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $PSScriptRoot
Set-Location $RootDir

$Bin = $env:BIN
if ([string]::IsNullOrWhiteSpace($Bin)) {
  $Bin = Join-Path $RootDir "bin\localgate.exe"
}
$Config = $env:CONFIG
if ([string]::IsNullOrWhiteSpace($Config)) {
  $Config = Join-Path $RootDir "localgate.yaml"
}

$TokenFile = $env:LOCALGATE_HTTP_BEARER_TOKEN_FILE
if ([string]::IsNullOrWhiteSpace($TokenFile)) {
  $TokenFile = Join-Path $RootDir ".localgate\http-bearer-token"
}
if ([string]::IsNullOrWhiteSpace($env:LOCALGATE_HTTP_BEARER_TOKEN) -and (Test-Path $TokenFile)) {
  $env:LOCALGATE_HTTP_BEARER_TOKEN = (Get-Content -Raw $TokenFile).Trim()
}

if ($env:SKIP_BUILD -ne "1") {
  & (Join-Path $PSScriptRoot "build.ps1")
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}

& $Bin -config $Config -mcp @args
exit $LASTEXITCODE
