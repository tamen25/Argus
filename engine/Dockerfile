# Argus engine image. Build context: engine/ directory.
#
# The build stage runs on the build host's platform and cross-compiles for the
# target (Go needs no emulator for that), so a multi-arch build runs no step
# under emulation. BuildKit sets BUILDPLATFORM, TARGETOS and TARGETARCH.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
# SPEC_VERSION bakes the Instrumentation Score spec pin into the binary —
# distroless has no repo checkout to read .instrumentation-score-version from.
ARG SPEC_VERSION=
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.embeddedSpecVersion=${SPEC_VERSION}" -o /out/argus ./cmd/argus

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/argus /usr/local/bin/argus
EXPOSE 8080
ENTRYPOINT ["argus"]
CMD ["serve"]
