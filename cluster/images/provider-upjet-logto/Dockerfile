FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY .work/bin/provider /usr/local/bin/provider
ENV TERRAFORM_PROVIDER_SOURCE=Lenstra/logto
ENV TERRAFORM_PROVIDER_VERSION=0.0.15
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/provider"]
