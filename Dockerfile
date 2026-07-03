FROM node:22-alpine AS ui-builder
WORKDIR /build/ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci
COPY ui/ ./
COPY ../cnak-design-system /build/cnak-design-system
RUN npm run build

FROM golang:1.24-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY --from=ui-builder /build/ui/dist ./ui/dist
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o gogo .

FROM gcr.io/distroless/static:nonroot
COPY --from=go-builder /build/gogo /gogo
ENTRYPOINT ["/gogo"]
