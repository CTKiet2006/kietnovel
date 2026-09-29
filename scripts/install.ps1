# Cài đặt / cập nhật kietnovel: build từ source rồi chép vào thư mục trên PATH.
#
#   powershell -ExecutionPolicy Bypass -File scripts\install.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -SkipBuild
$ErrorActionPreference = 'Stop'

$repo   = Split-Path -Parent $PSScriptRoot
$go     = 'C:\Program Files\Go\bin\go.exe'
$dest   = Join-Path $env:LOCALAPPDATA 'kietnovel\bin'
$target = Join-Path $dest 'kietnovel.exe'

if (-not (Test-Path $go)) {
    throw "Khong tim thay Go tai $go. Cai Go >= 1.25 truoc."
}

Push-Location $repo
try {
    if ($SkipBuild) {
        Write-Host 'Bo qua build (dung -SkipBuild)'
    } else {
        Write-Host 'Dang build...'
        & $go build -o kietnovel.exe ./cmd/kietnovel
        if ($LASTEXITCODE -ne 0) { throw "Build that bai (exit $LASTEXITCODE)" }
    }

    if (-not (Test-Path '.\kietnovel.exe')) { throw 'Khong tim thay kietnovel.exe' }

    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    # File dang chay khong ghi duoc tren Windows, xoa truoc.
    if (Test-Path $target) {
        try { Remove-Item $target -Force -ErrorAction Stop } catch {
            throw "Dang chay kietnovel. Hay thoat no truoc roi chay lai install.ps1."
        }
    }
    Copy-Item '.\kietnovel.exe' $target -Force
}
finally {
    Pop-Location
}

# Dam bao thu muc bin nam trong user PATH
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dest*") {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dest", 'User')
    Write-Host "Da them $dest vao user PATH (mo terminal moi de dung ngay)"
} else {
    Write-Host "$dest da co san trong PATH"
}

Write-Host ''
Write-Host "Da cai: $target"
& $target --version
Write-Host ''
Write-Host 'Chay tu bat ky thu muc nao:  kietnovel'
