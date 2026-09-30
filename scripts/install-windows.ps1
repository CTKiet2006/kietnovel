<#
.SYNOPSIS
    Cài kietnovel trên Windows mà không cần cài Go.

.DESCRIPTION
    Tải bản dựng sẵn đúng cho kiến trúc máy (x86_64 hoặc arm64), xác minh
    checksum SHA-256 do chính release công bố, giải nén vào
    %LOCALAPPDATA%\kietnovel\bin, rồi tự thêm thư mục đó vào user PATH nếu
    chưa có.

    Chạy:
        irm https://raw.githubusercontent.com/CTKiet2006/kietnovel/main/scripts/install-windows.ps1 | iex

    Hoặc tải file rồi chạy:
        powershell -ExecutionPolicy Bypass -File install-windows.ps1
#>
[CmdletBinding()]
param(
    # Bỏ qua bước thêm thư mục vào user PATH (dành cho ai muốn tự quản lý).
    [switch]$NoPath
)

$ErrorActionPreference = 'Stop'

# Chỉ chạy được trên Windows: script này tải .zip/.exe bản dựng sẵn.
if ($env:OS -ne 'Windows_NT') {
    throw 'Script nay chi dung tren Windows. Tren macOS/Linux, tai ban .tar.gz tu trang Release.'
}

# $PSVersionRoot khong ton tai o PS 2, nhung 3.0+ thi co. Dung $PSVersionTable cho
# chac chan, va fallback neu bien do khong co (chi xay ra o host la).
if ($PSVersionTable.PSVersion.Major -lt 3) {
    throw 'Can PowerShell 3.0 tro len.'
}

$repo      = 'CTKiet2006/kietnovel'
$dest      = Join-Path $env:LOCALAPPDATA 'kietnovel\bin'
$target    = Join-Path $dest 'kietnovel.exe'
$staging   = Join-Path ([System.IO.Path]::GetTempPath()) ('kietnovel-install-' + [guid]::NewGuid().ToString('N'))

function Write-Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok($msg)   { Write-Host "    $msg" -ForegroundColor Green }

# --- 1. Chon ban dung theo kien truc va he dieu hanh -------------------------

# $env:PROCESSOR_ARCHITECTURE chi doc duoc; tren ARM64 may co the bao AArch64 o
# PROCESSOR_ARCHITEW6432 khi chay x64 qua emulation, nen phai kiem tra ca hai.
$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -eq 'AMD64') { $goArch = 'x86_64' }
elseif ($env:PROCESSOR_ARCHITEW6432 -eq 'AMD64') { $goArch = 'x86_64' }
elseif ($arch -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $goArch = 'arm64' }
else { throw "Khong do duoc kien truc may (PROCESSOR_ARCHITECTURE=$arch)." }

Write-Step "Kien truc: $goArch"

# Chon ban moi nhat co asset Windows. Release tag duoc sap xep ban chay bang
# version, nen lay danh sach tag roi chon phan tuoi nhat - tranh tinh huong
# @latest cua proxy con tro tag cu.
Write-Step 'Tim ban moi nhat...'
$tags = Invoke-RestMethod -Headers @{ 'User-Agent' = 'kietnovel-installer' } `
                          -Uri "https://api.github.com/repos/$repo/releases"
if (-not $tags -or $tags.Count -eq 0) { throw 'Khong tim thay release nao tren GitHub.' }

$release = $tags | Where-Object { $_.draft -eq $false -and $_.prerelease -eq $false } |
           Select-Object -First 1
if (-not $release) { throw 'Khong tim thay ban phat hanh chinh thuc nao.' }

Write-Ok "$($release.tag_name)"

# --- 2. Tim asset + checksum dung -------------------------------------------

# GoReleaser loai bo chu "v" trong ten asset (ProjectName_Version_... voi
# Version khong co "v"), con tag cua GitHub thi co. Bo "v" de khop ten that.
$ver = $release.tag_name -replace '^v', ''

$fileName = "kietnovel_${ver}_Windows_$goArch.zip"
$asset    = $release.assets | Where-Object { $_.name -eq $fileName } | Select-Object -First 1
if (-not $asset) {
    $have = ($release.assets | ForEach-Object { $_.name }) -join ', '
    throw "Khong tim thay '$fileName' trong release. Release co: $have"
}

$sumAsset = $release.assets | Where-Object { $_.name -eq 'kietnovel_checksums.txt' } | Select-Object -First 1
if (-not $sumAsset) { throw 'Release khong co file checksums.txt, bo qua xac minh.' }

# --- 3. Tai va xac minh SHA-256 --------------------------------------------

New-Item -ItemType Directory -Path $staging -Force | Out-Null
try {
    Write-Step "Tai $fileName ..."
    $zipPath = Join-Path $staging $fileName
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zipPath -UseBasicParsing
    $mb = [math]::Round((Get-Item $zipPath).Length / 1MB, 1)
    Write-Ok "$mb MB"

    Write-Step 'Kiem tra checksum...'
    $sumPath = Join-Path $staging 'checksums.txt'
    Invoke-WebRequest -Uri $sumAsset.browser_download_url -OutFile $sumPath -UseBasicParsing

    # File checksum dung dinh dang: "<hex>  <filename>" (Hai khoang cach).
    $line = Select-String -Path $sumPath -Pattern ([regex]::Escape($fileName)) |
            Select-Object -First 1
    if (-not $line) { throw "Checksum khong co dong cho '$fileName'." }

    $expected = ($line.Line -split '\s+')[0].ToLower()
    $actual   = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
    if ($expected -ne $actual) {
        throw "Checksum khong khop. Tai ve: expected=$expected actual=$actual"
    }
    Write-Ok 'Checksum OK'

    # --- 4. Giai nen va dat binary ----------------------------------------

    Write-Step 'Giai nen...'
    Expand-Archive -Path $zipPath -DestinationPath $staging -Force
    $exe = Get-ChildItem $staging -Filter 'kietnovel.exe' -Recurse | Select-Object -First 1
    if (-not $exe) { throw 'Trong file zip khong co kietnovel.exe.' }

    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    if (Test-Path $target) {
        # Windows khong cho ghi de file dang chay, nen doi ten ban cu truoc.
        Write-Step 'Thay the ban cu...'
        try {
            Remove-Item $target -Force -ErrorAction Stop
        } catch {
            Rename-Item $target "$target.old" -Force -ErrorAction SilentlyContinue
        }
    }
    Move-Item $exe.FullName $target -Force
    Write-Ok $target

    # --- 5. Them vao user PATH --------------------------------------------

    if (-not $NoPath) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ($null -eq $userPath) { $userPath = '' }
        # So sanh khong phan biet hoa/thuong va bo qua dau chieu phan tach.
        $parts = $userPath -split ';' | Where-Object { $_ -ne '' }
        $has   = $parts | Where-Object { $_.TrimEnd('\') -ieq $dest.TrimEnd('\') }
        if (-not $has) {
            Write-Step 'Them thu muc vao user PATH...'
            $new = if ($userPath.TrimEnd(';') -eq '') { $dest } else { "$userPath;$dest" }
            [Environment]::SetEnvironmentVariable('Path', $new, 'User')
            Write-Ok 'Da them (mo terminal moi de co hieu luc)'
        } else {
            Write-Ok 'Thu muc da co san trong PATH'
        }
    }

    # --- 6. Bao cao ket qua -------------------------------------------------

    Write-Host ''
    Write-Ok 'Cai dat thanh cong.'
    & $target --version
    Write-Host ''
    Write-Host 'Mo terminal moi roi chay:' -ForegroundColor Cyan
    Write-Host '    kietnovel' -ForegroundColor White
    Write-Host 'Ban dau chay se hoi Provider, API key va ngon ngu (vi / en / zh).' -ForegroundColor DarkGray
}
finally {
    Remove-Item $staging -Recurse -Force -ErrorAction SilentlyContinue
}
