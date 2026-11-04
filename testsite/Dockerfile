# v2dock test site: a page that checks, from inside this container, which
# services it can reach.
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod ./
COPY main.go index.html ./
RUN go build -trimpath -ldflags "-s -w" -o /out/netcheck .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/netcheck /usr/local/bin/netcheck
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/netcheck"]
