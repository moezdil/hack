FROM golang:1.26 AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /hack .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /hack /hack
EXPOSE 8080
ENTRYPOINT ["/hack"]
