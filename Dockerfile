FROM --platform=$BUILDPLATFORM golang:1.27@sha256:3680233e3204827fbdc66088528ae6d4b3d034f51d03a99d454f6de034888244 AS build
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
FROM gcr.io/distroless/static:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087
COPY --from=build /out/gpukoll /gpukoll
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gpukoll"]
