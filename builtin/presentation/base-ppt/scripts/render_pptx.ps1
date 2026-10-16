[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$InputPptx,

    [Parameter(Mandatory = $true)]
    [string]$OutputDir,

    [int]$Width = 1600,
    [int]$Height = 900
)

$ErrorActionPreference = 'Stop'

$source = (Resolve-Path -LiteralPath $InputPptx).Path
$target = [System.IO.Path]::GetFullPath($OutputDir)

# PowerPoint automation can attach to an already-open interactive instance. Never
# risk closing or disturbing a presentation owned by the user during autonomous QA.
$runningPowerPoint = @(Get-Process -Name POWERPNT -ErrorAction SilentlyContinue)
if ($runningPowerPoint.Count -gt 0) {
    throw 'PowerPoint is already running; visual QA was not started to protect the user-owned Office session.'
}

New-Item -ItemType Directory -Force -Path $target | Out-Null

$powerPoint = $null
$presentation = $null
try {
    $powerPoint = New-Object -ComObject PowerPoint.Application
    $presentation = $powerPoint.Presentations.Open($source, $true, $false, $false)
    $slideCount = $presentation.Slides.Count

    # Export deterministic names one slide at a time. Presentation.Export uses
    # localized names and leaves stale files behind when a later deck has fewer
    # slides, which can turn a successful nine-slide render into a false
    # "10 PNG files for 9 slides" failure.
    Get-ChildItem -LiteralPath $target -File -Filter 'slide_*.png' -ErrorAction SilentlyContinue |
        Remove-Item -Force
    $images = @()
    for ($index = 1; $index -le $slideCount; $index++) {
        $imagePath = Join-Path $target ('slide_{0:D2}.png' -f $index)
        $slide = $null
        try {
            $slide = $presentation.Slides.Item($index)
            $slide.Export($imagePath, 'PNG', $Width, $Height)
        }
        finally {
            if ($slide) {
                [void][Runtime.InteropServices.Marshal]::ReleaseComObject($slide)
            }
        }
        if (-not (Test-Path -LiteralPath $imagePath -PathType Leaf)) {
            throw "PowerPoint did not export slide $index to $imagePath"
        }
        $images += $imagePath
    }

    Add-Type -AssemblyName System.Drawing
    $thumbWidth = 400
    $thumbHeight = [int][Math]::Round($thumbWidth * $Height / $Width)
    $columns = [Math]::Min(4, $images.Count)
    $rows = [int][Math]::Ceiling($images.Count / [double]$columns)
    $sheet = New-Object System.Drawing.Bitmap ($columns * $thumbWidth), ($rows * $thumbHeight)
    $graphics = [System.Drawing.Graphics]::FromImage($sheet)
    try {
        $graphics.Clear([System.Drawing.Color]::FromArgb(24, 28, 38))
        for ($index = 0; $index -lt $images.Count; $index++) {
            $preview = [System.Drawing.Image]::FromFile($images[$index])
            try {
                $x = ($index % $columns) * $thumbWidth
                $y = [Math]::Floor($index / $columns) * $thumbHeight
                $graphics.DrawImage($preview, $x, $y, $thumbWidth, $thumbHeight)
            }
            finally {
                $preview.Dispose()
            }
        }
        $contactSheet = Join-Path $target 'contact_sheet.jpg'
        $sheet.Save($contactSheet, [System.Drawing.Imaging.ImageFormat]::Jpeg)
    }
    finally {
        $graphics.Dispose()
        $sheet.Dispose()
    }

    [ordered]@{
        ok          = $true
        input       = $source
        output_dir  = $target
        slide_count = $slideCount
        width       = $Width
        height      = $Height
        images      = $images
        contact_sheet = $contactSheet
    } | ConvertTo-Json -Depth 4
}
finally {
    if ($presentation) {
        $presentation.Close()
        [void][Runtime.InteropServices.Marshal]::ReleaseComObject($presentation)
    }
    if ($powerPoint) {
        $powerPoint.Quit()
        [void][Runtime.InteropServices.Marshal]::ReleaseComObject($powerPoint)
    }
    [GC]::Collect()
    [GC]::WaitForPendingFinalizers()
}
