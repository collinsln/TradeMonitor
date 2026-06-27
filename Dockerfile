# Step 1: Build the Go binary
FROM golang:1.21-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the Go modules manifests first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application. (Adjust the output name if needed)
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o trademonitor .

# Step 2: Create the minimal runtime image
FROM alpine:latest  

# Add CA certificates for secure external connections
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copy the pre-built binary from the previous stage
COPY --from=builder /app/trademonitor .
COPY --from=builder /app/grid_trading.db . 

# Expose the port your Go app listens on (change 8080 if your app uses a different port)
EXPOSE 8080

# Command to run the executable
CMD ["./trademonitor"]
