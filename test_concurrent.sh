#!/bin/bash

# Test script to verify concurrent write fix

echo "Starting local test server on port 8081..."
python3 -m http.server 8081 &
SERVER_PID=$!

sleep 2

echo "Starting tunnel client..."
go run cmd/online/main.go expose 8081 &
CLIENT_PID=$!

sleep 3

echo "Making 10 concurrent requests..."
for i in {1..10}; do
    curl -s http://localhost:8080/test-$i &
done

wait

echo "Cleaning up..."
kill $CLIENT_PID
kill $SERVER_PID

echo "Test completed - if no panic occurred, the fix is working!"