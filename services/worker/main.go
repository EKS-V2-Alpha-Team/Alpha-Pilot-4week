package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	startMetricsServer(getEnv("METRICS_PORT", "9090"))

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
				workerReceiveErrors.Inc() // NEW
				log.Printf("Receive failed: %v", err)
				time.Sleep(5 * time.Second)
			}
			continue
		}

		for _, msg := range messages {
			var event Event
			if err := json.Unmarshal([]byte(aws.ToString(msg.Body)), &event); err != nil {
				workerEvents.WithLabelValues("unknown", "parse_failed").Inc() // NEW
				// Left on the queue: after 3 receives SQS moves it to the DLQ.
				log.Printf("Failed to parse message %s: %v", aws.ToString(msg.MessageId), err)
				continue
			}

			eventType := eventTypeLabel(event.Type) // NEW
			log.Printf("Processing event %s (%s)", event.Type, event.ID)

			start := time.Now()                                                                 // NEW
			handleErr := handleEvent(client, services, event)                                   // CHANGED
			workerEventDuration.WithLabelValues(eventType).Observe(time.Since(start).Seconds()) // NEW
			if handleErr != nil {
				workerEvents.WithLabelValues(eventType, "handle_failed").Inc() // NEW
				// Not deleted: SQS hands it out again after the visibility timeout
				// and moves it to the DLQ after 3 receives.
				log.Printf("Failed to handle event %s (%s): %v", event.Type, event.ID, handleErr)
				continue
			}

			if err := queue.delete(ctx, msg.ReceiptHandle); err != nil {
				workerEvents.WithLabelValues(eventType, "delete_failed").Inc() // NEW
				// Handled but not deleted: it comes back, which is safe because reserve,
				// charge and status changes can be repeated (ALPHA-51).
				log.Printf("Failed to delete event %s (%s): %v", event.Type, event.ID, err)
				continue
			}
			workerEvents.WithLabelValues(eventType, "success").Inc() // NEW
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
	case "order.status_changed":
		var change struct {
			OrderID   int    `json:"order_id"`
			NewStatus string `json:"new_status"`
		}
		if err := json.Unmarshal(event.Payload, &change); err != nil {
			return fmt.Errorf("bad order.status_changed payload: %w", err)
		}
		return handleStatusChange(client, services, change.OrderID, change.NewStatus)
	default:
		// No handler yet (ALPHA-78). Deleted so these don't fill the DLQ.
		log.Printf("  -> no handler for %s yet, skipping", event.Type)
		return nil
	}
}

// What each status change sets off (ALPHA-78). Every step is safe to repeat, so an error
// here just leaves the message on the queue for another go.
//
//	confirmed  -> "order confirmed" email
//	processing -> shipment created (label)
//	shipped    -> shipment in transit, "order shipped" email with the tracking number
//	delivered  -> shipment delivered, "order delivered" email
//	cancelled  -> stock and money back, shipment cancelled, "order cancelled" email
func handleStatusChange(client *http.Client, services map[string]string, orderID int, newStatus string) error {
	switch newStatus {
	case "cancelled":
		log.Printf("  -> order %d cancelled, giving back stock and money", orderID)
		if err := undoOrder(client, services, orderID); err != nil {
			return err
		}
		if err := cancelShipment(client, services, orderID); err != nil {
			return err
		}
		// Last, and not best effort: a failed email returns an error so the message comes
		// back. Every step above is safe to repeat and the email is sent once per order
		// (dedupe key), so a retry only finishes what is missing.
		order, err := getOrder(client, services, orderID)
		if err != nil {
			return err
		}
		return notifyCustomer(client, services, order, "order_cancelled", "")
	case "confirmed", "processing", "shipped", "delivered":
		// handled below
	default:
		log.Printf("  -> order %d moved to %q, nothing to do", orderID, newStatus)
		return nil
	}

	order, err := getOrder(client, services, orderID)
	if err != nil {
		return err
	}
	if order.Status == "cancelled" {
		// A late event for an order that has since been cancelled: no email, no shipment.
		log.Printf("  -> order %d moved to %q but is now cancelled, skipping", orderID, newStatus)
		return nil
	}

	switch newStatus {
	case "confirmed":
		return notifyCustomer(client, services, order, "order_confirmed", "")
	case "processing":
		return createShipment(client, services, order)
	case "shipped":
		// Create first: it does nothing when the shipment exists, and covers an order
		// that was moved on by hand without passing "processing".
		if err := createShipment(client, services, order); err != nil {
			return err
		}
		tracking, err := moveShipment(client, services, orderID, "in_transit")
		if err != nil {
			return err
		}
		return notifyCustomer(client, services, order, "order_shipped", tracking)
	default: // delivered
		if err := createShipment(client, services, order); err != nil {
			return err
		}
		if _, err := moveShipment(client, services, orderID, "delivered"); err != nil {
			return err
		}
		return notifyCustomer(client, services, order, "order_delivered", "")
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
	// The same message can come back after the order was already finished: a redelivery,
	// a DLQ redrive, or a crash before the delete. Check where the order is now and only
	// finish what is missing. A cancelled order is never reserved or charged again.
	status, err := getOrderStatus(client, services, o.OrderID)
	if err != nil {
		return err
	}
	switch status {
	case "pending":
		// Normal path below.
	case "cancelled":
		// Cancelled while this message was in flight (for example by hand after the
		// charge went through). Finish the undo so no money or stock is left behind.
		log.Printf("  -> order %d: already cancelled, undoing what was taken", o.OrderID)
		return undoOrder(client, services, o.OrderID)
	default:
		// confirmed or later: the charge already went through. Make sure the stock is
		// committed; committing twice changes nothing.
		log.Printf("  -> order %d: already %s, making sure its stock is committed", o.OrderID, status)
		return commitOrHoldAgain(client, services, o)
	}

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

	// Every retry of this order's charge uses the same key, so payment answers a retry
	// with the first result, a decline included, instead of trying the card again.
	code, err = sendJSONWithKey(client, http.MethodPost, services["payment"]+"/charge", chargeKey(o.OrderID), map[string]interface{}{
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
		if err := releaseStock(client, services, o.OrderID); err != nil {
			return err
		}
		if err := setOrderStatus(client, services, o.OrderID, "cancelled"); err != nil {
			return err
		}
		// Best effort: the order is already cancelled and its stock released, so a
		// failed email must not send this message round again.
		declined := orderInfo{ID: o.OrderID, CustomerID: o.CustomerID, Total: o.Total, Currency: o.Currency}
		if err := notifyCustomer(client, services, declined, "payment_failed", ""); err != nil {
			log.Printf("  -> order %d: payment_failed email not sent: %v", o.OrderID, err)
		}
		return nil
	default:
		return fmt.Errorf("charge: unexpected status %d", code)
	}

	if err := setOrderStatus(client, services, o.OrderID, "confirmed"); err != nil {
		return err
	}

	// Turn the held stock into a sale (ALPHA-77). Only after the confirm, so stock never
	// leaves the shelf for an order that isn't confirmed.
	return commitOrHoldAgain(client, services, o)
}

// commitOrHoldAgain commits the order's stock. If the hold has expired (inventory-service
// answers 409) the customer has already paid, so it takes the stock again; if there is none
// left it cancels the order, and the cancel handling refunds the charge. Without this a paid
// order could finish with the stock never leaving the shelf.
func commitOrHoldAgain(client *http.Client, services map[string]string, o newOrder) error {
	err := commitStock(client, services, o.OrderID)
	if !errors.Is(err, errHoldGone) {
		return err
	}
	log.Printf("  -> order %d: its stock hold expired before the commit, holding it again", o.OrderID)
	code, rerr := sendJSON(client, http.MethodPost, services["inventory"]+"/reserve", map[string]interface{}{
		"order_id": o.OrderID,
		"items":    o.Items,
	})
	if rerr != nil {
		return fmt.Errorf("reserve again: %w", rerr)
	}
	switch code {
	case http.StatusCreated, http.StatusOK:
		return commitStock(client, services, o.OrderID)
	case http.StatusConflict, http.StatusNotFound:
		log.Printf("  -> order %d: paid but the stock is gone, cancelling so it is refunded", o.OrderID)
		return setOrderStatus(client, services, o.OrderID, "cancelled")
	default:
		return fmt.Errorf("reserve again: unexpected status %d", code)
	}
}

// undoOrder gives a cancelled order back everything it took: releases stock still held,
// puts sold stock back on the shelf, and refunds the charge. Each step does nothing when
// there is nothing to undo and is safe to repeat, so it runs for every cancellation
// (including the worker's own) and again on a retry.
func undoOrder(client *http.Client, services map[string]string, orderID int) error {
	if err := releaseStock(client, services, orderID); err != nil {
		return err
	}
	if err := restockSold(client, services, orderID); err != nil {
		return err
	}
	return refundCharge(client, services, orderID)
}

// restockSold returns stock that was already sold (committed) for the order. Safe to repeat.
func restockSold(client *http.Client, services map[string]string, orderID int) error {
	code, err := sendJSON(client, http.MethodPost, services["inventory"]+"/restock", map[string]interface{}{
		"order_id": orderID,
	})
	if err != nil {
		return fmt.Errorf("restock: %w", err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("restock: unexpected status %d", code)
	}
	return nil
}

// refundCharge refunds the order's charge. 404 means the order was never charged
// (out of stock, declined, or cancelled before payment), so there is nothing to refund.
// Safe to repeat: payment-service answers 200 for a charge it already refunded.
func refundCharge(client *http.Client, services map[string]string, orderID int) error {
	code, err := sendJSON(client, http.MethodPost, services["payment"]+"/refund", map[string]interface{}{
		"order_id": orderID,
		"reason":   "order cancelled",
	})
	if err != nil {
		return fmt.Errorf("refund: %w", err)
	}
	if code != http.StatusOK && code != http.StatusNotFound {
		return fmt.Errorf("refund: unexpected status %d", code)
	}
	return nil
}

// orderInfo is the part of an order the worker needs for emails and shipments.
type orderInfo struct {
	ID         int     `json:"id"`
	CustomerID string  `json:"customer_id"`
	Status     string  `json:"status"`
	Total      float64 `json:"total"`
	Currency   string  `json:"currency"`
}

// getOrder asks order-service for the order as it is now.
func getOrder(client *http.Client, services map[string]string, orderID int) (orderInfo, error) {
	resp, err := client.Get(fmt.Sprintf("%s/%d", services["order"], orderID))
	if err != nil {
		return orderInfo{}, fmt.Errorf("get order: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return orderInfo{}, fmt.Errorf("get order: unexpected status %d", resp.StatusCode)
	}
	var order orderInfo
	if err := json.NewDecoder(resp.Body).Decode(&order); err != nil || order.Status == "" {
		return orderInfo{}, fmt.Errorf("get order: unreadable response")
	}
	order.ID = orderID
	return order, nil
}

// getOrderStatus asks order-service for the order's current status.
func getOrderStatus(client *http.Client, services map[string]string, orderID int) (string, error) {
	order, err := getOrder(client, services, orderID)
	return order.Status, err
}

// There is no address model yet (ALPHA-78), so every shipment gets this placeholder.
// The shipment, its tracking number and its status all work; only the address is empty.
const placeholderAddress = "PLACEHOLDER - no address collected yet (ALPHA-78)"

// createShipment makes the order's shipment. Safe to repeat: shipping-service keeps one
// shipment per order and answers 200 with the existing one.
func createShipment(client *http.Client, services map[string]string, o orderInfo) error {
	recipient := o.CustomerID
	if recipient == "" {
		recipient = "unknown customer"
	}
	code, err := sendJSON(client, http.MethodPost, services["shipping"]+"/shipments", map[string]interface{}{
		"order_id":       o.ID,
		"recipient_name": recipient,
		"address_line1":  placeholderAddress,
		"city":           "TBC",
		"postcode":       "TBC",
		"country":        "GB",
	})
	if err != nil {
		return fmt.Errorf("create shipment: %w", err)
	}
	if code != http.StatusCreated && code != http.StatusOK {
		return fmt.Errorf("create shipment: unexpected status %d", code)
	}
	return nil
}

// moveShipment moves the order's shipment to status ("in_transit" or "delivered") and
// returns its tracking number. Safe to repeat.
func moveShipment(client *http.Client, services map[string]string, orderID int, status string) (string, error) {
	var out struct {
		TrackingNumber string `json:"tracking_number"`
	}
	code, err := doJSON(client, http.MethodPut, fmt.Sprintf("%s/shipments/by-order/%d", services["shipping"], orderID),
		map[string]interface{}{"status": status}, &out)
	if err != nil {
		return "", fmt.Errorf("shipment %s: %w", status, err)
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("shipment %s: unexpected status %d", status, code)
	}
	return out.TrackingNumber, nil
}

// cancelShipment cancels the order's shipment. 404 means there never was one (the
// order was cancelled before it reached processing), so there is nothing to cancel.
func cancelShipment(client *http.Client, services map[string]string, orderID int) error {
	code, err := sendJSON(client, http.MethodPut, fmt.Sprintf("%s/shipments/by-order/%d", services["shipping"], orderID),
		map[string]interface{}{"status": "cancelled"})
	if err != nil {
		return fmt.Errorf("cancel shipment: %w", err)
	}
	if code != http.StatusOK && code != http.StatusNotFound {
		return fmt.Errorf("cancel shipment: unexpected status %d", code)
	}
	return nil
}

// notifyCustomer sends the email for template. The dedupe key (template + order) means
// notification-service sends it once however many times the message comes back.
func notifyCustomer(client *http.Client, services map[string]string, o orderInfo, template, tracking string) error {
	if o.CustomerID == "" {
		log.Printf("  -> order %d has no customer to email, skipping %s", o.ID, template)
		return nil
	}
	name := o.CustomerID
	if at := strings.Index(name, "@"); at > 0 {
		name = name[:at]
	}
	code, err := sendJSON(client, http.MethodPost, services["notification"]+"/send", map[string]interface{}{
		"recipient":  o.CustomerID,
		"channel":    "email",
		"template":   template,
		"dedupe_key": fmt.Sprintf("%s:%d", template, o.ID),
		"data": map[string]interface{}{
			"OrderID":        o.ID,
			"CustomerName":   name,
			"Currency":       o.Currency,
			"Total":          fmt.Sprintf("%.2f", o.Total),
			"TrackingNumber": tracking,
		},
	})
	if err != nil {
		return fmt.Errorf("send %s: %w", template, err)
	}
	if code != http.StatusCreated && code != http.StatusOK {
		return fmt.Errorf("send %s: unexpected status %d", template, code)
	}
	return nil
}

// releaseStock releases any stock still held for the order. Safe to repeat.
func releaseStock(client *http.Client, services map[string]string, orderID int) error {
	code, err := sendJSON(client, http.MethodPost, services["inventory"]+"/release", map[string]interface{}{
		"order_id": orderID,
	})
	if err != nil {
		return fmt.Errorf("release stock: %w", err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("release stock: unexpected status %d", code)
	}
	return nil
}

// commitStock turns the order's held stock into a sale. Safe to repeat.
func commitStock(client *http.Client, services map[string]string, orderID int) error {
	code, err := sendJSON(client, http.MethodPost, services["inventory"]+"/commit", map[string]interface{}{
		"order_id": orderID,
	})
	if err != nil {
		return fmt.Errorf("commit stock: %w", err)
	}
	if code == http.StatusConflict {
		return errHoldGone
	}
	if code != http.StatusOK {
		return fmt.Errorf("commit stock: unexpected status %d", code)
	}
	return nil
}

// errHoldGone means inventory-service has no active hold to commit for the order.
var errHoldGone = errors.New("commit stock: the hold is gone")

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
	return doJSON(client, method, url, body, nil)
}

// chargeKey is the Idempotency-Key for an order's charge: the same for every retry.
func chargeKey(orderID int) string { return fmt.Sprintf("charge-order-%d", orderID) }

// sendJSONWithKey is sendJSON with an Idempotency-Key header.
func sendJSONWithKey(client *http.Client, method, url, key string, body interface{}) (int, error) {
	return doRequest(client, method, url, map[string]string{"Idempotency-Key": key}, body, nil)
}

// doJSON is sendJSON that also decodes a JSON response into out (when out is not nil
// and the response is 200). A response that can't be decoded leaves out empty.
func doJSON(client *http.Client, method, url string, body interface{}, out interface{}) (int, error) {
	return doRequest(client, method, url, nil, body, out)
}

func doRequest(client *http.Client, method, url string, headers map[string]string, body interface{}, out interface{}) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		json.NewDecoder(resp.Body).Decode(out)
	}
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
