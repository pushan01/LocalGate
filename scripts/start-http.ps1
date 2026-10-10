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

if ($env:DAEMON -eq "1") {
  throw "Windows daemon mode is not implemented by start-http.ps1 yet; run in the foreground or use Windows Service/Task Scheduler."
}

$ArgsList = @("-config", $Config)
if (-not [string]::IsNullOrWhiteSpace($env:BIND)) {
  $ArgsList += @("-bind", $env:BIND)
}
if (-not [string]::IsNullOrWhiteSpace($env:ADMIN_BIND)) {
  $ArgsList += @("-admin-bind", $env:ADMIN_BIND)
}
if (-not [string]::IsNullOrWhiteSpace($env:PUBLIC_DEPLOYMENTS_BIND)) {
  $ArgsList += @("-public-deployments-bind", $env:PUBLIC_DEPLOYMENTS_BIND)
}
if (-not [string]::IsNullOrWhiteSpace($env:PRIVATE_DEPLOYMENTS_BIND)) {
  $ArgsList += @("-private-deployments-bind", $env:PRIVATE_DEPLOYMENTS_BIND)
}

$env:LOCALGATE_RUNTIME_MODE = "http_foreground"
$env:LOCALGATE_RUNTIME_BIN = $Bin
$env:LOCALGATE_RUNTIME_CONFIG = $Config
$env:LOCALGATE_RUNTIME_BIND = $env:BIND
$env:LOCALGATE_RUNTIME_ADMIN_BIND = $env:ADMIN_BIND
$env:LOCALGATE_RUNTIME_PUBLIC_DEPLOYMENT_BIND = $env:PUBLIC_DEPLOYMENTS_BIND
$env:LOCALGATE_RUNTIME_PRIVATE_DEPLOYMENT_BIND = $env:PRIVATE_DEPLOYMENTS_BIND

& $Bin @ArgsList @args
exit $LASTEXITCODE
