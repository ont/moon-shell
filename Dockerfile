FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/moon-shell .

FROM alpine:3.20

RUN apk add --no-cache \
    bash \
    ca-certificates \
    coreutils \
    curl \
    file \
    findutils \
    gzip \
    jq \
    nmap \
    nmap-ncat \
    openssh-client \
    tar \
    unzip \
    xz \
    zip \
 && apk add --no-cache \
    --repository=https://dl-cdn.alpinelinux.org/alpine/edge/main \
    --repository=https://dl-cdn.alpinelinux.org/alpine/edge/community \
    --repository=https://dl-cdn.alpinelinux.org/alpine/edge/testing \
    atool

#RUN adduser -D -u 10001 appuser
#USER appuser

WORKDIR /app

COPY --from=build /out/moon-shell /app/moon-shell

EXPOSE 8080

ENTRYPOINT ["/app/moon-shell"]
