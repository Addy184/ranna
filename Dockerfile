FROM golang:1.16-alpine AS build
WORKDIR /build
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
COPY go.mod .
COPY go.sum .
COPY scripts/ scripts/
COPY .git/ .git/
RUN apk add git
RUN sh ./scripts/populateinfo.sh
RUN go build -o ranna cmd/ranna/main.go

FROM alpine:latest AS final
COPY --from=build /build/ranna /bin/ranna
COPY spec/ spec/
RUN chmod +x /bin/ranna
ENTRYPOINT ["/bin/ranna"]