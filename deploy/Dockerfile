FROM golang:1.23 AS build
ARG SERVICE
WORKDIR /src
COPY go.mod ./
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -o /bin/service ./cmd/$SERVICE

FROM gcr.io/distroless/static-debian12
COPY --from=build /bin/service /service
ENTRYPOINT ["/service"]
