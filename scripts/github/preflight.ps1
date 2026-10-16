[CmdletBinding()]
param(
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path

Push-Location $repoRoot
try {
    Write-Host "[1/6] Checking patch whitespace..."
    git diff --check
    if ($LASTEXITCODE -ne 0) {
        throw "git diff --check failed"
    }
    $existingCommits = @(git rev-list --max-count=1 --all)
    if ($existingCommits.Count -gt 0) {
        git diff --cached --check
        if ($LASTEXITCODE -ne 0) {
            throw "git diff --cached --check failed"
        }
    } else {
        Write-Host "Initial snapshot: skipping incremental cached whitespace check."
    }

    Write-Host "[2/6] Checking repository boundaries..."
    $trackedLocal = @(git -c core.quotePath=false ls-files -- .local)
    if ($trackedLocal.Count -gt 0) {
        throw ".local contains tracked files: $($trackedLocal -join ', ')"
    }

    $trackedIgnored = @(git -c core.quotePath=false ls-files -ci --exclude-standard)
    if ($trackedIgnored.Count -gt 0) {
        $preview = $trackedIgnored | Select-Object -First 20
        $suffix = if ($trackedIgnored.Count -gt $preview.Count) {
            "`n... and $($trackedIgnored.Count - $preview.Count) more"
        } else {
            ""
        }
        throw "Git still tracks files excluded by .gitignore:`n$($preview -join "`n")$suffix"
    }

    Write-Host "[3/6] Checking GitHub file-size limit..."
    $oversized = @(
        git -c core.quotePath=false ls-files --cached --others --exclude-standard | ForEach-Object {
            if (Test-Path -LiteralPath $_ -PathType Leaf) {
                $item = Get-Item -LiteralPath $_
                if ($item.Length -ge 100MB) {
                    "{0:N1} MB`t{1}" -f ($item.Length / 1MB), $_
                }
            }
        }
    )
    if ($oversized.Count -gt 0) {
        throw "GitHub rejects files of 100 MB or more:`n$($oversized -join "`n")"
    }

    Write-Host "[4/6] Running release credential guard..."
    go run ./cmd/releaseguard
    if ($LASTEXITCODE -ne 0) {
        throw "release credential guard failed"
    }

    Write-Host "[5/6] Parsing maintained Python scripts..."
    Get-ChildItem scripts -Recurse -File -Filter *.py | ForEach-Object {
        python -X utf8 -c `
            "import ast,pathlib,sys; ast.parse(pathlib.Path(sys.argv[1]).read_text(encoding='utf-8'))" `
            $_.FullName
        if ($LASTEXITCODE -ne 0) {
            throw "Python syntax check failed: $($_.FullName)"
        }
    }

    if (-not $SkipTests) {
        Write-Host "[6/6] Running Go tests..."
        go test ./... -count=1 -failfast
        if ($LASTEXITCODE -ne 0) {
            throw "Go tests failed"
        }
    } else {
        Write-Host "[6/6] Go tests skipped by request."
    }

    Write-Host "Preflight passed. Review 'git status' and run a dedicated secret/history scan before a public push."
}
finally {
    Pop-Location
}
