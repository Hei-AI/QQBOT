@echo off
setlocal
set GOOS=linux
set GOARCH=amd64
go build -buildvcs=false -o build/QqBot
endlocal
