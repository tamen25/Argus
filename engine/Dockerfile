# Argus engine image. Build context: engine/ directory.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/argus ./cmd/argus

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/argus /usr/local/bin/argus
EXPOSE 8080
ENTRYPOINT ["argus"]
CMD ["serve"]
