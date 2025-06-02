FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build -o goping github.com/alexraskin/goping

FROM alpine

RUN apk --no-cache add ca-certificates

COPY --from=build /build/goping /bin/goping

EXPOSE 8080

ENTRYPOINT ["/bin/goping"]

CMD ["-metrics-port", "8080"]
