[CmdletBinding()]
param(
    [switch]$CompileOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

function Invoke-QualityCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Description,

        [Parameter(Mandatory = $true)]
        [scriptblock]$Command
    )

    Write-Host "==> $Description"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
}

Push-Location $repoRoot
try {
    Invoke-QualityCommand -Description 'release credential guard' -Command {
        go run ./cmd/releaseguard
    }
    Invoke-QualityCommand -Description 'module checksum verification' -Command {
        go mod verify
    }

    Write-Host '==> gofmt check'
    $goFiles = @(
        @(
            git ls-files -- '*.go'
            git ls-files --others --exclude-standard -- '*.go'
        ) |
            Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } |
            Sort-Object -Unique
    )
    if ($LASTEXITCODE -ne 0) {
        throw "list tracked Go files failed with exit code $LASTEXITCODE"
    }
    $unformatted = @()
    if ($goFiles.Count -gt 0) {
        $batchSize = 100
        for ($offset = 0; $offset -lt $goFiles.Count; $offset += $batchSize) {
            $last = [Math]::Min($offset + $batchSize - 1, $goFiles.Count - 1)
            $batch = @($goFiles[$offset..$last])
            $unformatted += @(& gofmt -l @batch)
            if ($LASTEXITCODE -ne 0) {
                throw "gofmt check failed for batch beginning at index $offset with exit code $LASTEXITCODE"
            }
        }
    }
    $legacyGofmt = [System.Collections.Generic.HashSet[string]]::new(
        [System.StringComparer]::OrdinalIgnoreCase
    )
    foreach ($line in [System.IO.File]::ReadAllLines((Join-Path $repoRoot 'scripts/gofmt_legacy.txt'))) {
        $entry = $line.Trim().Replace('\', '/')
        if ($entry -and -not $entry.StartsWith('#')) {
            [void]$legacyGofmt.Add($entry)
        }
    }
    $newUnformatted = @(
        $unformatted |
            ForEach-Object { $_.Trim().Replace('\', '/') } |
            Where-Object { $_ -and -not $legacyGofmt.Contains($_) }
    )
    if ($newUnformatted.Count -gt 0) {
        throw "gofmt required for new or changed files:`n$($newUnformatted -join "`n")"
    }
    if ($unformatted.Count -gt 0) {
        Write-Host "legacy gofmt debt: $($unformatted.Count) file(s), no new violations"
    }

    Invoke-QualityCommand -Description 'whitespace check' -Command {
        git diff --check
    }
    Invoke-QualityCommand -Description 'staged whitespace check' -Command {
        git diff --cached --check
    }
    Invoke-QualityCommand -Description 'go vet' -Command {
        go vet ./...
    }
    if ($CompileOnly) {
        Invoke-QualityCommand -Description 'package compilation' -Command {
            go test ./... -run '^$' -count=1
        }
    } else {
        Invoke-QualityCommand -Description 'full Go test suite' -Command {
            go test ./... -count=1
        }
    }
    Invoke-QualityCommand -Description 'all-package build' -Command {
        go build -buildvcs=false -trimpath ./...
    }
    Write-Host 'quality gate: PASS'
} finally {
    Pop-Location
}
