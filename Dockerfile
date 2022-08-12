FROM golang:1.18 AS build-env
ARG version
RUN apt-get update && apt-get install gcc libc-dev
ADD . /src/proxy
WORKDIR /src/proxy
RUN go mod vendor
RUN go build -ldflags "-s -w" -o proxy ./cmd/main.go

FROM golang:1.18

COPY --from=build-env /src/proxy/proxy /app/

COPY --from=build-env /src/proxy/files/countries.json /app/files/countries.json
COPY --from=build-env /src/proxy/files/regions.json /app/files/regions.json
COPY --from=build-env /src/proxy/files/cities.json /app/files/cities.json

WORKDIR /app

ENTRYPOINT ["./proxy"]