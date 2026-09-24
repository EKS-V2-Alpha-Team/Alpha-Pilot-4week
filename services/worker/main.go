package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Event is the envelope order-service publishes (see order-service outbox.go).
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp string          `json:"timestamp"`
}

func main() {
	sqsQueueURL := os.Getenv("SQS_QUEUE_URL")
	if sqsQueueURL == "" {
		log.Fatal("SQS_QUEUE_URL is required")
	}

	// Internal service URLs for event-driven calls
	services := map[string]string{
		"inventory":    getEnv("INVENTORY_SERVICE_URL", "http://inventory-service:8082"),
		"payment":      getEnv("PAYMENT_SERVICE_URL", "http://payment-service:8083"),
		"notification": getEnv("NOTIFICATION_SERVICE_URL", "http://notification-service:8084"),
		"shipping":     getEnv("SHIPPING_SERVICE_URL", "http://shipping-service:8085"),
		"order":        getEnv("ORDER_SERVICE_URL", "http://order-service:8081"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Region, credentials and AWS_ENDPOINT_URL (Floci locally) come from the standard AWS env vars.
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	queue := &sqsQueue{client: sqs.NewFromConfig(awsCfg), url: sqsQueueURL}

	// Health check endpoint
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "worker"})
		})
		port := getEnv("HEALTH_PORT", "8090")
		log.Printf("Worker health check on :%s", port)
		http.ListenAndServe(":"+port, mux)
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down worker...")
		cancel()
	}()

	log.Println("Worker started, polling SQS for events...")
	pollAndProcess(ctx, queue, services)
}

func pollAndProcess(ctx context.Context, queue *sqsQueue, services map[string]string) {
	client := &http.Client{Timeout: 10 * time.Second}

	for ctx.Err() == nil {
		messages, err := queue.receive(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("Receive failed: %v", err)
				time.Sleep(5 * time.Second)
			}
			continue
		}

		for _, msg := range messages {
			var event Event
			if err := json.Unmarshal([]byte(aws.ToString(msg.Body)), &event); err != nil {
				// Left on the queue: after 3 receives SQS moves it to the DLQ.
				log.Printf("Failed to parse message %s: %v", aws.ToString(msg.MessageId), err)
				continue
			}

			log.Printf("Processing event %s (%s)", event.Type, event.ID)

			if err := handleEvent(client, services, event); err != nil {
				// Not deleted: SQS hands it out again after the visibility timeout
				// and moves it to the DLQ after 3 receives.
				log.Printf("Failed to handle event %s (%s): %v", event.Type, event.ID, err)
				continue
			}

			if err := queue.delete(ctx, msg.ReceiptHandle); err != nil {
				// Handled but not deleted: it comes back, which is safe because reserve,
				// charge and status changes can be repeated (ALPHA-51).
				log.Printf("Failed to delete event %s (%s): %v", event.Type, event.ID, err)
				continue
			}
			log.Printf("Done: %s (%s)", event.Type, event.ID)
		}
	}
	log.Println("Worker stopped")
}

func handleEvent(client *http.Client, services map[string]string, event Event) error {
	switch event.Type {
	case "order.created":
		var order newOrder
		if err := json.Unmarshal(event.Payload, &order); err != nil {
			return fmt.Errorf("bad order.created payload: %w", err)
		}
		return processNewOrder(client, services, order)
	default:
		// No handler yet (ALPHA-78). Deleted so these don't fill the DLQ.
		log.Printf("  -> no handler for %s yet, skipping", event.Type)
		return nil
	}
}

type newOrder struct {
	OrderID    int     `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	Total      float64 `json:"total"`
	Currency   string  `json:"currency"`
	Items      []struct {
		ProductID string `json:"product_id"`
		Quantity  int    `json:"quantity"`
	} `json:"items"`
}

// processNewOrder takes a new order through reserve -> charge -> confirm -> commit stock.
//
// Out of stock or a declined card is a final answer: undo what was done, cancel the
// order and return nil so the message is deleted. Anything else returns an error so
// SQS retries it. Every call is safe to repeat (ALPHA-51), so a retry after a partial
// run picks up where it left off.
func processNewOrder(client *http.Client, services map[string]string, o newOrder) error {
	code, err := sendJSON(client, http.MethodPost, services["inventory"]+"/reserve", map[string]interface{}{
		"order_id": o.OrderID,
		"items":    o.Items,
	})
	if err != nil {
		return fmt.Errorf("reserve: %w", err)
	}
	switch code {
	case http.StatusCreated, http.StatusOK:
	case http.StatusConflict, http.StatusNotFound:
		log.Printf("  -> order %d: not enough stock, cancelling", o.OrderID)
		return setOrderStatus(client, services, o.OrderID, "cancelled")
	default:
		return fmt.Errorf("reserve: unexpected status %d", code)
	}

	code, err = sendJSON(client, http.MethodPost, services["payment"]+"/charge", map[string]interface{}{
		"order_id":    o.OrderID,
		"customer_id": o.CustomerID,
		"amount":      o.Total,
		"currency":    o.Currency,
		"method":      "card",
	})
	if err != nil {
		return fmt.Errorf("charge: %w", err)
	}
	switch code {
	case http.StatusCreated, http.StatusOK:
	case http.StatusPaymentRequired:
		log.Printf("  -> order %d: payment declined, releasing stock and cancelling", o.OrderID)
		code, err := sendJSON(client, http.MethodPost, services["inventory"]+"/release", map[string]interface{}{
			"order_id": o.OrderID,
		})
		if err != nil {
			return fmt.Errorf("release after decline: %w", err)
		}
		if code != http.StatusOK {
			return fmt.Errorf("release after decline: unexpected status %d", code)
		}
		return setOrderStatus(client, services, o.OrderID, "cancelled")
	default:
		return fmt.Errorf("charge: unexpected status %d", code)
	}

	if err := setOrderStatus(client, services, o.OrderID, "confirmed"); err != nil {
		return err
	}

	// Turn the held stock into a sale (ALPHA-77). Only after the confirm, so stock never
	// leaves the shelf for an order that isn't confirmed.
	code, err = sendJSON(client, http.MethodPost, services["inventory"]+"/commit", map[string]interface{}{
		"order_id": o.OrderID,
	})
	if err != nil {
		return fmt.Errorf("commit stock: %w", err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("commit stock: unexpected status %d", code)
	}
	return nil
}

func setOrderStatus(client *http.Client, services map[string]string, orderID int, status string) error {
	code, err := sendJSON(client, http.MethodPut, services["order"]+"/status", map[string]interface{}{
		"order_id":   orderID,
		"new_status": status,
	})
	if err != nil {
		return fmt.Errorf("set status %s: %w", status, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("set status %s: unexpected status %d", status, code)
	}
	return nil
}

// sendJSON sends body as JSON and returns the response status code.
func sendJSON(client *http.Client, method, url string, body interface{}) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

type sqsQueue struct {
	client *sqs.Client
	url    string
}

// receive long-polls for up to 10 messages. It returns early when ctx is cancelled,
// so SIGTERM doesn't wait out the 20 s poll.
func (q *sqsQueue) receive(ctx context.Context) ([]types.Message, error) {
	out, err := q.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(q.url),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     20,
	})
	if err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (q *sqsQueue) delete(ctx context.Context, receiptHandle *string) error {
	// Finish the delete even if shutdown started while the message was being handled.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := q.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(q.url),
		ReceiptHandle: receiptHandle,
	})
	return err
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
