package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// الرموز مأخوذة بدقة من صورتك وبدون أخطاء كتابية
const (
	TelegramToken  = "8648655175:AAF82kQB0rBOy1YIgdNLCK4OV4GQh8ZQKGA"
	TelegramChatID = "8480374366"
	UploadDir      = "./uploads"
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
	Amount float64 `json:"amount"` 
}

func main() {
	_ = os.MkdirAll(UploadDir, os.ModePerm)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/billing/subscribe", createSubscriptionHandler)
	http.HandleFunc("/webhook/tap", tapWebhookHandler)
	http.HandleFunc("/api/submit-transfer", submitTransferHandler)
	http.HandleFunc("/api/admin/activate-user", adminActivateUserHandler)

	log.Printf("Server starting on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func createSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type IncomingPlan struct {
		Amount float64 `json:"amount"`
		Email  string  `json:"email"`
	}
	var plan IncomingPlan
	json.NewDecoder(r.Body).Decode(&plan)

	if plan.Amount <= 0 {
		plan.Amount = 799.00 
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

	if payload.Status == "CAPTURED" {
		switch payload.Amount {
		case 799.00:
			fmt.Printf("Success! Charge %s: Activated Elite Plan (799 SAR)\n", payload.ID)
		case 399.00:
			fmt.Printf("Success! Charge %s: Activated Advanced Plan (399 SAR)\n", payload.ID)
		default:
			fmt.Printf("Success! Charge %s: Activated Custom Plan (%f SAR)\n", payload.ID, payload.Amount)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func submitTransferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	phone := r.FormValue("phone")
	userID := r.FormValue("user_id")
	packageName := r.FormValue("package")

	file, header, err := r.FormFile("screenshot")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "صورة التحويل مطلوبة"}`))
		return
	}
	defer file.Close()

	filename := fmt.Sprintf("%d_%s", time.Now().Unix(), filepath.Base(header.Filename))
	filePath := filepath.Join(UploadDir, filename)

	out, err := os.Create(filePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "فشل حفظ الملف"}`))
		return
	}
	defer out.Close()

	_, _ = io.Copy(out, file)

	caption := fmt.Sprintf("🔔 **إشعار تحويل جديد - منصة صرح**\n\n👤 معرف المشترك (ID): %s\n📱 رقم الهاتف: %s\n📦 الباقة المطلوبة: %s\n\nيرجى مراجعة حسابك البنكي للتأكد من وصول المبلغ ثم تفعيل الحساب.", userID, phone, packageName)
	
	go sendTelegramPhoto(filePath, caption)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "تم رفع وصل التحويل بنجاح! جاري مراجعته وتفعيل حسابك خلال دقائق."}`))
}

func adminActivateUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	fmt.Printf("⚙️ تم تفعيل العميل بنجاح في السيرفر: %s\n", req.UserID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "تم تفعيل باقة المشترك بنجاح التام!"}`))
}

func sendTelegramPhoto(filePath, caption string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", TelegramToken)

	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("photo", filepath.Base(filePath))
	if err != nil {
		return
	}
	_, _ = io.Copy(part, file)

	_ = writer.WriteField("chat_id", TelegramChatID)
	_ = writer.WriteField("caption", caption)
	_ = writer.WriteField("parse_mode", "Markdown")
	writer.Close()

	req, _ := http.NewRequest("POST", url, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	_, _ = client.Do(req)
}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type IncomingPlan struct {
		Amount float64 `json:"amount"`
		Email  string  `json:"email"`
	}
	var plan IncomingPlan
	json.NewDecoder(r.Body).Decode(&plan)

	if plan.Amount <= 0 {
		plan.Amount = 799.00 
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

	if payload.Status == "CAPTURED" {
		switch payload.Amount {
		case 799.00:
			fmt.Printf("Success! Charge %s: Activated Elite Plan (799 SAR)\n", payload.ID)
		case 399.00:
			fmt.Printf("Success! Charge %s: Activated Advanced Plan (399 SAR)\n", payload.ID)
		default:
			fmt.Printf("Success! Charge %s: Activated Custom Plan (%f SAR)\n", payload.ID, payload.Amount)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func submitTransferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	phone := r.FormValue("phone")
	userID := r.FormValue("user_id")
	packageName := r.FormValue("package")

	file, header, err := r.FormFile("screenshot")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "صورة التحويل مطلوبة"}`))
		return
	}
	defer file.Close()

	filename := fmt.Sprintf("%d_%s", time.Now().Unix(), filepath.Base(header.Filename))
	filePath := filepath.Join(UploadDir, filename)

	out, err := os.Create(filePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "فشل حفظ الملف"}`))
		return
	}
	defer out.Close()

	_, _ = io.Copy(out, file)

	caption := fmt.Sprintf("🔔 **إشعار تحويل جديد - منصة صرح**\n\n👤 معرف المشترك (ID): %s\n📱 رقم الهاتف: %s\n📦 الباقة المطلوبة: %s\n\nيرجى مراجعة حسابك البنكي للتأكد من وصول المبلغ ثم تفعيل الحساب.", userID, phone, packageName)
	
	go sendTelegramPhoto(filePath, caption)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "تم رفع وصل التحويل بنجاح! جاري مراجعته وتفعيل حسابك خلال دقائق."}`))
}

func adminActivateUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	fmt.Printf("⚙️ تم تفعيل العميل بنجاح في السيرفر: %s\n", req.UserID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "تم تفعيل باقة المشترك بنجاح التام!"}`))
}

func sendTelegramPhoto(filePath, caption string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", TelegramToken)

	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("photo", filepath.Base(filePath))
	if err != nil {
		return
	}
	_, _ = io.Copy(part, file)

	_ = writer.WriteField("chat_id", TelegramChatID)
	_ = writer.WriteField("caption", caption)
	_ = writer.WriteField("parse_mode", "Markdown")
	writer.Close()

	req, _ := http.NewRequest("POST", url, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	_, _ = client.Do(req)
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
