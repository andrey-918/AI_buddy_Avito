FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/ai-buddy ./cmd/buddy

FROM alpine:3.21
RUN apk add --no-cache python3
WORKDIR /app
COPY --from=build /out/ai-buddy ./ai-buddy
COPY dist ./dist
COPY ml ./ml
COPY data/knowledge.json ./data/knowledge.json
ENV PORT=4173 BUDDY_DATA_FILE=/app/state/mock-state.json BUDDY_STATIC_DIR=/app/dist BUDDY_KNOWLEDGE_FILE=/app/data/knowledge.json
EXPOSE 4173
CMD ["./ai-buddy"]
