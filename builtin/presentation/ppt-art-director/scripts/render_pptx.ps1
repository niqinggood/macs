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

    # Presentation.Export is locale-sensitive and treats dot-prefixed output
    # directories inconsistently on some Office builds. It can place localized
    # files such as "幻灯片1.PNG" beside the PPTX while reporting no images in
    # the requested directory. Export every slide to an explicit deterministic
    # filename instead, so the render receipt is independent of Office locale.
    Get-ChildItem -LiteralPath $target -File -Filter 'slide_*.png' -ErrorAction SilentlyContinue |
        Remove-Item -Force
    $images = @()
    $pixelChecks = @()
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
        $imageInfo = Get-Item -LiteralPath $imagePath
        if ($imageInfo.Length -lt 2048) {
            throw "Rendered slide $index is unexpectedly small ($($imageInfo.Length) bytes)"
        }
        $images += $imagePath
    }

    Add-Type -AssemblyName System.Drawing

    # Validate the actual exported pixels before issuing a visual-QA receipt.
    # The structural auditor owns overlap/bounds/text-density checks; this pass
    # proves every page was rendered, is decodable, has the requested canvas,
    # and is not an empty/corrupt image. It is fully local and uploads nothing.
    for ($index = 0; $index -lt $images.Count; $index++) {
        $bitmap = New-Object System.Drawing.Bitmap $images[$index]
        try {
            if ($bitmap.Width -ne $Width -or $bitmap.Height -ne $Height) {
                throw "Rendered slide $($index + 1) has $($bitmap.Width)x$($bitmap.Height) pixels; expected ${Width}x${Height}"
            }
            $sampledColors = New-Object 'System.Collections.Generic.HashSet[int]'
            $stepX = [Math]::Max(1, [int][Math]::Floor($bitmap.Width / 40))
            $stepY = [Math]::Max(1, [int][Math]::Floor($bitmap.Height / 24))
            for ($y = 0; $y -lt $bitmap.Height; $y += $stepY) {
                for ($x = 0; $x -lt $bitmap.Width; $x += $stepX) {
                    [void]$sampledColors.Add($bitmap.GetPixel($x, $y).ToArgb())
                }
            }
            $pixelChecks += [ordered]@{
                slide          = $index + 1
                width          = $bitmap.Width
                height         = $bitmap.Height
                bytes          = (Get-Item -LiteralPath $images[$index]).Length
                sampled_colors = $sampledColors.Count
                decodable      = $true
            }
        }
        finally {
            $bitmap.Dispose()
        }
    }

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
        pixel_checks = $pixelChecks
        slides_rendered = $slideCount
        pixel_integrity_checked = $true
        all_slide_images_decodable = $true
        presentation_visual_inspected = $false
        visual_inspection_required = $true
        pixel_integrity_scope = 'native render and image decodability only; use audit_pptx.py for structure and an actual image/vision tool for composition, clipping, overlap, and aesthetics'
    } | ConvertTo-Json -Depth 4 -Compress
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
