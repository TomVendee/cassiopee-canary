# Étape 1 : compilation. CGO_ENABLED=0 produit un binaire statique, qui tourne
# sans aucune bibliothèque système dans l'image finale.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /canary .

# Étape 2 : image finale, sans shell ni gestionnaire de paquets (~15 Mo).
# USER 1000:1000 correspond au securityContext imposé par l'intranet
# (runAsNonRoot, runAsUser 1000). EXPOSE 8080 permet à Cassiopée de deviner
# le port quand le champ « port cible » reste vide.
FROM gcr.io/distroless/static-debian13
COPY --from=build /canary /canary
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/canary"]
