#Requires -Version 5.1
<#
.SYNOPSIS
    Creates icon.ico from a PNG source file.
    Generates 16x16, 32x32, 48x48, 256x256 sizes.
.USAGE
    .\tools\create-ico.ps1 -SourcePNG ..\logo-square.png
#>
param(
    [string]$SourcePNG = "..\logo-square.png",
    [string]$OutputICO = "..\icon.ico"
)

Add-Type -AssemblyName System.Drawing

$src = Resolve-Path $SourcePNG -ErrorAction Stop
$img = [System.Drawing.Image]::FromFile($src)

$sizes = @(256, 48, 32, 16)

# ICO file format: header + directory + bitmap data
$ms = New-Object System.IO.MemoryStream
$bw = New-Object System.IO.BinaryWriter($ms)

# Reserve space for header (6 bytes) + directory entries (16 bytes each)
$headerSize  = 6
$dirEntrySize= 16
$dataOffset  = $headerSize + $dirEntrySize * $sizes.Count

$bitmapData = @()
foreach ($size in $sizes) {
    $bmp = New-Object System.Drawing.Bitmap($size, $size)
    $g   = [System.Drawing.Graphics]::FromImage($bmp)
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.DrawImage($img, 0, 0, $size, $size)
    $g.Dispose()

    $pngMs = New-Object System.IO.MemoryStream
    $bmp.Save($pngMs, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose()
    $bitmapData += ,@{ Size=$size; Data=$pngMs.ToArray() }
    $pngMs.Dispose()
}

# Write ICO header
$bw.Write([uint16]0)              # reserved
$bw.Write([uint16]1)              # type: ICO
$bw.Write([uint16]$sizes.Count)   # image count

# Write directory entries
$offset = $dataOffset
foreach ($entry in $bitmapData) {
    $sz  = $entry.Size
    $len = $entry.Data.Length
    $bw.Write([byte]  ($sz -eq 256 ? 0 : $sz))  # width (0 = 256)
    $bw.Write([byte]  ($sz -eq 256 ? 0 : $sz))  # height
    $bw.Write([byte]  0)     # color count
    $bw.Write([byte]  0)     # reserved
    $bw.Write([uint16]1)     # color planes
    $bw.Write([uint16]32)    # bits per pixel
    $bw.Write([uint32]$len)  # data size
    $bw.Write([uint32]$offset)
    $offset += $len
}

# Write bitmap data
foreach ($entry in $bitmapData) {
    $bw.Write($entry.Data)
}

$bw.Flush()
$icoBytes = $ms.ToArray()
$bw.Dispose()
$ms.Dispose()
$img.Dispose()

[System.IO.File]::WriteAllBytes((Resolve-Path (Split-Path $OutputICO) | Join-Path -ChildPath (Split-Path $OutputICO -Leaf)), $icoBytes)
Write-Host "Created $OutputICO ($([math]::Round($icoBytes.Length/1024,1)) KB)" -ForegroundColor Green
Write-Host "Now run: go generate" -ForegroundColor Cyan
