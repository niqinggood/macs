[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Destination,
    [string[]]$IncludeUntracked = @(),
    [switch]$PlanOnly
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$destinationPath = [System.IO.Path]::GetFullPath($Destination)
$pathComparison = [System.StringComparison]::OrdinalIgnoreCase
$rootPrefix = $repoRoot.TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar

$destinationAncestor = $destinationPath
while ($destinationAncestor) {
    if (Test-Path -LiteralPath $destinationAncestor) {
        $ancestorItem = Get-Item -LiteralPath $destinationAncestor -Force
        if ($ancestorItem.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw "Destination must not resolve through a linked directory"
        }
    }
    $destinationAncestor = Split-Path -Parent $destinationAncestor
}

if ($destinationPath.Equals($repoRoot, $pathComparison)) {
    throw "Destination must not be the source repository"
}
if ($destinationPath.StartsWith($rootPrefix, $pathComparison)) {
    $relativeDestination = $destinationPath.Substring($rootPrefix.Length).Replace('\', '/')
    git -C $repoRoot check-ignore --no-index -q -- "$relativeDestination/.snapshot-probe"
    if ($LASTEXITCODE -ne 0) {
        throw "A destination inside the repository must be ignored, such as .local/public-previews/"
    }
}
if (Test-Path -LiteralPath $destinationPath) {
    $destinationItem = Get-Item -LiteralPath $destinationPath -Force
    if (-not $destinationItem.PSIsContainer -or
        ($destinationItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "Destination must be an ordinary directory"
    }
    if (@(Get-ChildItem -LiteralPath $destinationPath -Force).Count -gt 0) {
        throw "Destination must not exist or must be empty: $destinationPath"
    }
}

function Get-CheckedSourceFile {
    param([string]$RelativePath)
    if ([IO.Path]::IsPathRooted($RelativePath) -or
        $RelativePath -match '(^|[\\/])\.\.([\\/]|$)') {
        throw "Snapshot paths must stay inside the source repository"
    }
    $sourcePath = [IO.Path]::GetFullPath((Join-Path $repoRoot $RelativePath))
    if (-not $sourcePath.StartsWith($rootPrefix, $pathComparison)) {
        throw "Snapshot source escaped the repository"
    }
    if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
        return $null
    }
    $sourceItem = Get-Item -LiteralPath $sourcePath -Force
    $ancestor = $sourceItem
    while ($null -ne $ancestor -and -not $ancestor.FullName.Equals($repoRoot, $pathComparison)) {
        if ($ancestor.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw "Review linked files separately before publishing: $RelativePath"
        }
        $ancestor = if ($ancestor.PSIsContainer) { $ancestor.Parent } else { $ancestor.Directory }
    }
    if ($sourceItem.Length -gt 100MB) {
        throw "A source file exceeds GitHub's 100 MiB limit: $RelativePath"
    }
    return $sourceItem
}

$ignoredTracked = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
git -C $repoRoot -c core.quotePath=false ls-files -ci --exclude-standard | ForEach-Object {
    [void]$ignoredTracked.Add($_)
}
if ($LASTEXITCODE -ne 0) { throw "Unable to enumerate ignored tracked files" }
$tracked = @(git -C $repoRoot -c core.quotePath=false ls-files --cached)
if ($LASTEXITCODE -ne 0) { throw "Unable to enumerate tracked files" }
$untracked = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
git -C $repoRoot -c core.quotePath=false ls-files --others --exclude-standard | ForEach-Object {
    [void]$untracked.Add($_)
}
if ($LASTEXITCODE -ne 0) { throw "Unable to enumerate untracked files" }

$selected = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($relativePath in $tracked) {
    if (-not $ignoredTracked.Contains($relativePath)) { [void]$selected.Add($relativePath) }
}
foreach ($requestedPath in $IncludeUntracked) {
    $normalizedPath = $requestedPath.Replace('\', '/')
    if (-not $untracked.Contains($normalizedPath)) {
        throw "IncludeUntracked must name an existing, non-ignored, untracked file: $normalizedPath"
    }
    [void]$selected.Add($normalizedPath)
}

$files = @(
    foreach ($relativePath in ($selected | Sort-Object)) {
        $item = Get-CheckedSourceFile $relativePath
        if ($null -ne $item) {
            [pscustomobject]@{ path = $relativePath; bytes = $item.Length }
        }
    }
)
if ($PlanOnly) {
    [pscustomobject]@{
        mode = "plan_only"
        files = $files
        file_count = $files.Count
        total_bytes = ($files | Measure-Object bytes -Sum).Sum
        excluded_ignored_tracked = $ignoredTracked.Count
        excluded_untracked = $untracked.Count - $IncludeUntracked.Count
    } | ConvertTo-Json -Depth 4
    return
}

# The source index and history remain untouched. No remote is configured here.
New-Item -ItemType Directory -Path $destinationPath -Force | Out-Null
foreach ($file in $files) {
    $sourceItem = Get-CheckedSourceFile $file.path
    if ($null -eq $sourceItem) { throw "A planned source file disappeared: $($file.path)" }
    $targetPath = [IO.Path]::GetFullPath((Join-Path $destinationPath $file.path))
    $destinationPrefix = $destinationPath.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (-not $targetPath.StartsWith($destinationPrefix, $pathComparison)) {
        throw "Snapshot target escaped the destination"
    }
    New-Item -ItemType Directory -Path (Split-Path -Parent $targetPath) -Force | Out-Null
    Copy-Item -LiteralPath $sourceItem.FullName -Destination $targetPath
}
git -C $destinationPath init -b main
if ($LASTEXITCODE -ne 0) { throw "git init failed in the snapshot" }
git -C $destinationPath add --all
if ($LASTEXITCODE -ne 0) { throw "git add failed in the snapshot" }
Write-Host "Created a review snapshot with $($files.Count) files: $destinationPath"
Write-Host "No old history, commit, remote, or push was created. Review content, retain the license notices, and run preflight before publishing."
