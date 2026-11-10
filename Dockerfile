FROM --platform=$BUILDPLATFORM golang:1.27@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e AS build
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
FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/gpukoll /gpukoll
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gpukoll"]
