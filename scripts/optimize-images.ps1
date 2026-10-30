# 把 _rawimg 里的原图批量转成 webp 并落到 content/img。
# 一次性脚本：原图来自 cmd/crawler，转换后前端只引用 content/img 下的文件。
#
# 用法：powershell -File scripts/optimize-images.ps1
param(
    [string]$RawDir = "$PSScriptRoot/../_rawimg",
    [string]$OutDir = "$PSScriptRoot/../content/img",
    [int]$MaxWidth = 1200,
    [int]$Quality = 80
)

$ErrorActionPreference = 'Stop'
$RawDir = (Resolve-Path $RawDir).Path
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$OutDir = (Resolve-Path $OutDir).Path

$ffmpeg = (Get-Command ffmpeg -ErrorAction SilentlyContinue).Source
if (-not $ffmpeg) { throw 'ffmpeg 不在 PATH 中' }

$files = @(Get-ChildItem $RawDir -File | Where-Object { $_.Extension -ne '.part' })
Write-Host "convert $($files.Count) images -> $OutDir (max ${MaxWidth}px, q$Quality)"

$inSum = [int64]0
$outSum = [int64]0
$failed = @()
$i = 0

foreach ($f in $files) {
    $i++
    if ($i % 50 -eq 0) { Write-Host "  $i / $($files.Count)" }

    $ext = $f.Extension.ToLowerInvariant()
    $inSum += $f.Length

    # gif 保留动画、svg 是矢量图，都原样拷贝（ffmpeg 转不了 svg）。
    if (($ext -eq '.gif') -or ($ext -eq '.svg')) {
        $dest = Join-Path $OutDir $f.Name
        if (-not (Test-Path $dest)) { Copy-Item $f.FullName $dest }
        $outSum += (Get-Item $dest).Length
        continue
    }

    $destName = [System.IO.Path]::GetFileNameWithoutExtension($f.Name) + '.webp'
    $dest = Join-Path $OutDir $destName
    if ((Test-Path $dest) -and (Get-Item $dest).Length -gt 0) {
        $outSum += (Get-Item $dest).Length
        continue
    }

    & $ffmpeg -hide_banner -loglevel error -y -i $f.FullName `
        -vf "scale='min($MaxWidth,iw)':-1:flags=lanczos" `
        -c:v libwebp -quality $Quality -compression_level 6 $dest 2>&1 | Out-Null

    if ((Test-Path $dest) -and (Get-Item $dest).Length -gt 0) {
        $outSum += (Get-Item $dest).Length
    }
    else {
        $failed += $f.Name
    }
}

Write-Host ("done: {0} files, {1:N1} MB -> {2:N1} MB ({3:P0} 压缩)" -f $files.Count, ($inSum / 1MB), ($outSum / 1MB), (1 - $outSum / [double]$inSum))
if ($failed.Count -gt 0) {
    Write-Warning "$($failed.Count) 张失败："
    $failed | ForEach-Object { Write-Warning "  $_" }
}
