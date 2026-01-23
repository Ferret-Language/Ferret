@echo off
setlocal

set ROOT=%~dp0..
for %%I in ("%ROOT%") do set ROOT=%%~fI

echo Building Go WASM compiler...
set GOCACHE=%ROOT%\.gocache
set GOOS=js
set GOARCH=wasm
go build -o "%ROOT%\bin\ferret.wasm" "%ROOT%\main_wasm.go"
if errorlevel 1 (
  echo Failed to build wasm compiler.
  exit /b 1
)

set WEBSITE_ROOT=%ROOT%\..\website
set WEBSITE_PUBLIC=%WEBSITE_ROOT%\public
if exist "%WEBSITE_PUBLIC%" (
  copy /Y "%ROOT%\bin\ferret.wasm" "%WEBSITE_PUBLIC%\ferret2.wasm" >nul
  echo Copied wasm compiler to %WEBSITE_PUBLIC%\ferret2.wasm
) else (
  echo Website public folder not found: %WEBSITE_PUBLIC%
  echo Built wasm compiler at %ROOT%\bin\ferret.wasm
)

if exist "%WEBSITE_ROOT%" (
  if not exist "%WEBSITE_ROOT%\src\lib" (
    mkdir "%WEBSITE_ROOT%\src\lib"
  )
  copy /Y "%ROOT%\runtime\wasm\runtime.ts" "%WEBSITE_ROOT%\src\lib\runtime.ts" >nul
  echo Copied runtime TS to %WEBSITE_ROOT%\src\lib\runtime.ts
  copy /Y "%ROOT%\runtime\wasm\runtime_abi.ts" "%WEBSITE_ROOT%\src\lib\runtime_abi.ts" >nul
  echo Copied runtime ABI TS to %WEBSITE_ROOT%\src\lib\runtime_abi.ts
)

endlocal
