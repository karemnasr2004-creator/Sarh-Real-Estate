package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

// هيكل البيانات لطلب دفع مرن يقبل أي باقة وأي سعر
type ChargeRequest struct {
	Amount   float64  `json:"amount"`
	Currency string   `json:"currency"`
	Customer Customer `json:"customer"`
	Redirect Redirect `json:"redirect"`
}

type Customer struct {
	FirstName string `json:"first_name"`
	Email     string `json:"email"`
}

type Redirect struct {
	URL string `json:"url"`
}

// هيكل استقبال بيانات الـ Webhook الشامل لكافة الباقات
type TapWebhookPayload struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Amount float64 `json:"amount"` // قراءة المبلغ المدفوع تلقائياً لمعرفة نوع الباقة
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/billing/subscribe", createSubscriptionHandler)
	http.HandleFunc("/webhook/tap", tapWebhookHandler)

	log.Printf("Server starting on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

// 1. مسار مرن لتوليد الفاتورة بناءً على الباقة المختارة
func createSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// قراءة السعر ونوع الباقة المرسلة من الواجهة الأمامية (Front-end)
	type IncomingPlan struct {
		Amount float64 `json:"amount"`
		Email  string  `json:"email"`
	}
	var plan IncomingPlan
	json.NewDecoder(r.Body).Decode(&plan)

	if plan.Amount <= 0 {
		plan.Amount = 799.00 // القيمة الافتراضية لباقة نخبة إذا لم يحدد العميل
	}

	apiKey := os.Getenv("TAP_SECRET_KEY")

	chargeData := ChargeRequest{
		Amount:   plan.Amount,
		Currency: "SAR", 
		Customer: Customer{
			FirstName: "Sarh Customer",
			Email:     plan.Email, 
		},
		Redirect: Redirect{
			URL: "https://your-platform.com/dashboard", 
		},
	}

	jsonData, _ := json.Marshal(chargeData)

	req, _ := http.NewRequest("POST", "https://api.tap.company/v2/charges", bytes.NewBuffer(jsonData))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to connect to Tap", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// 2. مسار الـ Webhook الذكي الذي يفعل الحساب بناءً على قيمة الباقة تلقائياً
func tapWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	receivedSecret := r.Header.Get("X-Tap-Secret")
	if receivedSecret != os.Getenv("WEBHOOK_SECRET") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload TapWebhookPayload
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// إذا تمت العملية بنجاح، نقرأ المبلغ لتحديد الباقة وتفعيلها فوراً
	if payload.Status == "CAPTURED" {
		
		switch payload.Amount {
		case 799.00:
			// 🚀 تفعيل باقة "نخبة" في قاعدة البيانات
			fmt.Printf("Success! Charge %s: Activated Elite Plan (799 SAR)\n", payload.ID)
			
		case 399.00:
			// 🚀 تفعيل باقة "متقدمة" (مثال لباقة أخرى)
			fmt.Printf("Success! Charge %s: Activated Advanced Plan (399 SAR)\n", payload.ID)
			
		default:
			// 🚀 تفعيل أي باقة أخرى مخصصة بناءً على السيرفر
			fmt.Printf("Success! Charge %s: Activated Custom Plan (%f SAR)\n", payload.ID, payload.Amount)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}
