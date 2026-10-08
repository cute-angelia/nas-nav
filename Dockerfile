FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
COPY web ./web
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /nav .

FROM alpine:3.20
WORKDIR /app
COPY --from=build /nav /app/nav
RUN mkdir -p /data
ENV PORT=8787 DATA_DIR=/data
EXPOSE 8787
CMD ["/app/nav"]
