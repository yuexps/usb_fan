@echo off
chcp 65001 > nul
echo ==================================================
echo   Go 交叉编译: Windows -^> Linux AMD64 (FNOS)
echo   目标路径: app\server\usb_fan
echo ==================================================

set GOOS=linux
set GOARCH=amd64

go build -ldflags="-s -w" -o app\server\usb_fan main.go

if %ERRORLEVEL% NEQ 0 goto COMPILE_FAIL

echo "[成功] 交叉编译顺利完成！"
echo "产物路径: app\server\usb_fan"
echo ==================================================
echo   FNOS 应用打包 (fnpack.exe build)
echo ==================================================

fnpack.exe build

if %ERRORLEVEL% NEQ 0 goto PACK_FAIL

echo "[成功] FNOS 应用打包完成！"
goto END

:COMPILE_FAIL
echo "[错误] 编译失败，请检查上面输出的 Go 错误信息！"
goto END

:PACK_FAIL
echo "[错误] FNOS 应用打包失败，请检查上面输出！"
goto END

:END
echo ==================================================
pause
