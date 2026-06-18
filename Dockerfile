FROM golang:1.25 AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/polar ./cmd/polar

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/polar /usr/local/bin/polar
CMD ["/usr/local/bin/polar"]
