# syntax=docker/dockerfile:1.3

## Base

# Keep in step with the go directive in go.mod and the go version in mise.toml. The golang images
# set GOTOOLCHAIN=local, so this tag is the toolchain.
ARG GO_IMAGE_TAG=1.26.9-alpine3.24
FROM --platform=$BUILDPLATFORM golang:${GO_IMAGE_TAG} as base

ENV GOPRIVATE=github.com/project-init
FROM alpine:3.24
