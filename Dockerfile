# Used by GoReleaser's dockers_v2 section: prebuilt binaries are placed in
# the build context under $TARGETPLATFORM (e.g. linux/amd64/gossm), so no Go
# toolchain stage is needed.
#
# The base image must ship glibc and CA certificates: gossm extracts and runs
# the AWS session-manager-plugin, which is dynamically linked against glibc
# (scratch/alpine/distroless-static cannot run it).
FROM gcr.io/distroless/base-debian12:latest

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/gossm /usr/local/bin/gossm

ENTRYPOINT ["/usr/local/bin/gossm"]
