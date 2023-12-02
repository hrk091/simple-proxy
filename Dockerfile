FROM golang:1.21 AS build
WORKDIR /work

COPY *.go ./
ARG GOOS=linux
ARG GOARCH=amd64
RUN CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags="-s -w" -trimpath -o server main.go


FROM gcr.io/distroless/static-debian11:nonroot
COPY --from=build /work/server /

ENTRYPOINT [ "/server" ]
