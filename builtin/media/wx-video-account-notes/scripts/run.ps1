param(
    [Parameter(Mandatory = $true)]
    [string]$ShareUrl,

    [Parameter(Mandatory = $false)]
    [string]$OutputDir
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'common.ps1')

if (-not (Get-UvCommand) -or -not (Get-VenvPython)) {
    Write-Info 'Private runtime is missing; bootstrapping once.'
    & (Join-Path $PSScriptRoot 'bootstrap.ps1')
    if ($LASTEXITCODE -ne 0) {
        throw "Runtime bootstrap failed with exit code $LASTEXITCODE"
    }
}

$pipeline = Join-Path $PSScriptRoot 'invoke_pipeline.ps1'
if ($OutputDir) {
    & $pipeline -ShareUrl $ShareUrl -OutputDir $OutputDir
} else {
    & $pipeline -ShareUrl $ShareUrl
}
if ($LASTEXITCODE -ne 0) {
    throw "Media pipeline failed with exit code $LASTEXITCODE"
}
