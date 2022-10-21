#!/usr/bin/env sh
set -eu
protoc -I=../iscp-proto/go -I=${GOPATH}/src --gogofast_out=:autogen ../iscp-proto/go/extensions/*.proto
protoc -I=../iscp-proto/go -I=${GOPATH}/src \
--gogofast_out=Mextensions/downstream.proto=github.com/aptpod/iscp-go/encoding/autogen/extensions,\
Mextensions/connection.proto=github.com/aptpod/iscp-go/encoding/autogen/extensions,\
Mextensions/e2e_call.proto=github.com/aptpod/iscp-go/encoding/autogen/extensions,\
Mextensions/ping_pong.proto=github.com/aptpod/iscp-go/encoding/autogen/extensions,\
Mextensions/upstream.proto=github.com/aptpod/iscp-go/encoding/autogen/extensions:autogen ../iscp-proto/go/*.proto
