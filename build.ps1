<#
  Собирает две версии Equinox, каждая в своём архиве:
  для ПК (окно и трей) и серверную (без окна).

    .\build.ps1              проверки, тесты, сборка, установщик dist\Equinox-Setup-<версия>.exe (и его копия Equinox-Setup.exe
                             для уже установленных программ, см. internal/update), архивы Equinox-pc-<версия>.zip и Equinox-server-<версия>.zip
    .\build.ps1 -SkipTests   то же без запуска тестов (быстрее)

  Результат лежит в папке dist (она не входит в репозиторий).
#>
param([switch]$SkipTests)
$Version = (Get-Content -LiteralPath (Join-Path $PSScriptRoot VERSION) -Raw).Trim()

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
# Pure Go, always: on a machine with a C compiler in PATH, `go build` silently enables cgo and
# links against MinGW's runtime DLLs, which a plain Windows install does not have (v1.0.9 shipped
# broken this way when built on a GitHub Actions runner, which has one; explicit beats implicit).
$env:CGO_ENABLED = '0'

function Step($text) { Write-Host "==> $text" -ForegroundColor Cyan }
function Check($what) { if ($LASTEXITCODE -ne 0) { throw "$what завершилась с ошибкой (код $LASTEXITCODE)" } }

Step "gofmt"
$unformatted = gofmt -l cmd internal
if ($unformatted) { throw "Не отформатированы файлы:`n$($unformatted -join "`n")" }

Step "go vet"
go vet ./...
Check "go vet"

if (-not $SkipTests) {
    Step "тесты"
    go test -count=1 ./...
    Check "go test"
}

New-Item -ItemType Directory -Force -Path dist | Out-Null
# File names carry the version, so a rebuild for a new version would leave the previous one's files beside
# them (and in the checksums): clear what this script makes. dist\beta and the like are not touched.
Get-ChildItem dist -File | Where-Object { $_.Name -like 'Equinox*' -or $_.Name -eq 'SHA256SUMS.txt' } | Remove-Item -Force

Step "Equinox.exe (версия для ПК: окно и трей)"
go build -ldflags "-H windowsgui -s -w -X github.com/Ozzvin/equinox/internal/buildinfo.Version=$Version" -o dist\Equinox.exe .\cmd\equinox
Check "сборка Equinox.exe"

Step "Equinox-server.exe (серверная версия: без окна)"
go build -ldflags "-s -w -X github.com/Ozzvin/equinox/internal/buildinfo.Version=$Version" -o dist\Equinox-server.exe .\cmd\equinox-server
Check "сборка Equinox-server.exe"

Step "установщик (Inno Setup)"
$iscc = @(
    "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
    "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
    "$env:ProgramFiles\Inno Setup 6\ISCC.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if ($iscc) {
    $ver = $Version
    & $iscc /Qp "/DAppVersion=$ver" installer\equinox.iss
    Check "сборка установщика"
} else {
    Write-Host "Inno Setup не найден, установщик пропущен (winget install JRSoftware.InnoSetup)" -ForegroundColor Yellow
}

Step "архивы"
$pcReadme = @'
Equinox для ПК
===============

Запуск:   двойной клик по Equinox.exe. Установка не нужна, ключ вводить не надо.
Данные:   папка data рядом с программой (настройки, список раздач, загрузки, журнал).
          Чтобы перенести программу вместе с раздачами, копируйте папку целиком.
Трей:     закрытие окна прячет программу в трей, раздачи продолжаются.
          Полностью выйти: правый клик по значку в трее -> «Выход».
Требуется: Windows 10/11 и Microsoft Edge WebView2 Runtime
          (есть в Windows 11 и в большинстве Windows 10).

Удаление: выйдите из программы и удалите папку. Если включали автозапуск,
          сначала снимите галочку «Запускать вместе с Windows» в меню трея.
'@

$serverReadme = @'
Equinox Server
===============

Запуск:   Equinox-server.exe (двойной клик или из консоли). Окна нет: запускается движок,
          и в браузере по умолчанию сам открывается веб-интерфейс. Ключ вводить не надо:
          он уже в открытой ссылке, браузер его запомнит.
          Повторный запуск не создаёт вторую копию, а открывает страницу работающей.
Параметры: -state <папка>   где лежат данные (по умолчанию data рядом с программой)
           -listen <адрес>  адрес интерфейса, только localhost (по умолчанию 127.0.0.1:9091)
           -no-browser      не открывать браузер; тогда в консоли печатается полная ссылка с ключом
           -add <файл или magnet>  добавить раздачу при запуске
Остановка: Ctrl+C в окне консоли.
Ключ:     файл data\api-token; никому его не показывайте.
'@

function Pack($exe, $nameInZip, $readme, $zip) {
    $stage = Join-Path $env:TEMP "equinox-pack-$([guid]::NewGuid().ToString('N'))"
    New-Item -ItemType Directory -Path $stage | Out-Null
    Copy-Item $exe (Join-Path $stage $nameInZip)
    Set-Content -Encoding UTF8 -Path (Join-Path $stage 'README.txt') -Value $readme
    Compress-Archive -Force -Path (Join-Path $stage '*') -DestinationPath $zip
    Remove-Item -Recurse -Force $stage
}
# The portable programs carry the version in their file name too ("Equinox 1.0.17.exe"); the installed
# one stays Equinox.exe, which the shortcuts, autostart and file associations point at.
Pack dist\Equinox.exe "Equinox $Version.exe" $pcReadme "dist\Equinox-pc-$Version.zip"
Pack dist\Equinox-server.exe "Equinox-server $Version.exe" $serverReadme "dist\Equinox-server-$Version.zip"

# The installer is also published under its old fixed name: programs installed before names carried the
# version look for exactly that file when they update themselves (see internal/update).
if (Test-Path "dist\Equinox-Setup-$Version.exe") {
    Copy-Item "dist\Equinox-Setup-$Version.exe" dist\Equinox-Setup.exe
}

Step "контрольные суммы"
$sums = Get-ChildItem dist -File | Where-Object { $_.Extension -in ".exe", ".zip" } | ForEach-Object { "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name }
Set-Content -Encoding ASCII -Path dist\SHA256SUMS.txt -Value $sums

Write-Host ""
Get-ChildItem dist | Select-Object Name, @{n = 'МБ'; e = { [math]::Round($_.Length / 1MB, 1) }} | Format-Table -AutoSize
Write-Host "Готово: dist\Equinox-Setup-$Version.exe (и Equinox-Setup.exe), Equinox-pc-$Version.zip, Equinox-server-$Version.zip" -ForegroundColor Green
