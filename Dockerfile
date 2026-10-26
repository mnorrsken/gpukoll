FROM --platform=$BUILDPLATFORM golang:1.27@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/mnorrsken/gpukoll/internal/version.Version=${VERSION} -X github.com/mnorrsken/gpukoll/internal/version.Commit=${COMMIT}" \
    -o /out/gpukoll ./cmd/gpukoll

# distroless/static runs as a numeric non-root user, and the binary works
# under OpenShift's random UID as well.
FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /out/gpukoll /gpukoll
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gpukoll"]
