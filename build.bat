@echo off
setlocal enabledelayedexpansion

set ROOT_DIR=%~dp0
pushd "%ROOT_DIR%" >nul
if errorlevel 1 (
  echo Failed to enter %ROOT_DIR%
  exit /b 1
)

go run ./bundler
if errorlevel 1 (
  echo Build failed
  popd >nul
  exit /b 1
)

echo Self-contained bundle ready at output\ferret
popd >nul
exit /b 0
