@echo off
rem Build the reftest reference decoder on Windows (MSVC).
rem Run from a "x64 Native Tools Command Prompt for Visual Studio",
rem or after invoking vcvars64.bat.
setlocal
cd /d "%~dp0"

if not "%~1"=="" (
    set "SRC=%~1"
) else if exist 7zip-src\C (
    set "SRC=7zip-src"
) else (
    echo ^>^> cloning 7-Zip C sources ^(sparse^) into 7zip-src 1>&2
    rmdir /s /q 7zip-src 2>nul
    git clone --quiet --depth 1 --filter=blob:none --sparse https://github.com/ip7z/7zip 7zip-src || exit /b 1
    git -C 7zip-src sparse-checkout set C || exit /b 1
    set "SRC=7zip-src"
)

if not exist "%SRC%\C\Ppmd8.c" (
    echo error: %SRC%\C\Ppmd8.c not found ^(bad 7-Zip source path?^) 1>&2
    exit /b 1
)

echo ^>^> building refdec.exe 1>&2
cl /nologo /O2 /I "%SRC%\C" refdec.c "%SRC%\C\Ppmd8.c" "%SRC%\C\Ppmd8Dec.c" /Ferefdec.exe || exit /b 1

echo ^>^> done. Example: 1>&2
echo    refdec.exe archive.zip 40 100 4194304 0 99 1 1>&2
endlocal
