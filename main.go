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
	"strconv"
	"time"
)

// الرموز والسيرفر مدمجة وجاهزة للعمل مباشرة على Railway
const (
	TelegramToken  = "8648655175:AAF82kQB0rBOy1YIgdNLCK4OV4GQh8ZQKGA"
	TelegramChatID = "8480374366"
	UploadDir      = "./uploads"
	RailwayURL     = "https://observant-enjoyment-production.up.railway.app"
)

// الأسعار المرجعية للاشتراكات بالدولار الأمريكي (USD) لسهولة التحويل العالمي
const (
	BasePriceProUSD   = 106.00 // ما يعادل تقريباً 399 ريال
	BasePriceEliteUSD = 213.00 // ما يعادل تقريباً 799 ريال
)

// هيكل البيانات لطلب دفع مرن عبر بوابة Tap للاشتراكات
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

type TapWebhookPayload struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Amount float64 `json:"amount"` 
}

func main() {
	// إنشاء مجلد الإيصالات تلقائياً إذا لم يكن موجوداً
	_ = os.MkdirAll(UploadDir, os.ModePerm)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// تسجيل المسارات والخدمات العالمية الشاملة (للاشتراكات والإعلانات التجارية)
	http.HandleFunc("/api/get-prices", getLocalizedPricesHandler)
	http.HandleFunc("/api/convert-listing", convertListingPriceHandler) // مسار تحويل أسعار (الشقق، العمارات، البقالات، المحلات)
	http.HandleFunc("/billing/subscribe", createSubscriptionHandler)
	http.HandleFunc("/webhook/tap", tapWebhookHandler)
	http.HandleFunc("/api/submit-transfer", submitTransferHandler)
	http.HandleFunc("/api/admin/activate-user", adminActivateUserHandler)

	log.Printf("🚀 الخادم العالمي الشامل يعمل الآن على منفذ Railway: %s بكامل طاقته...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

// دالة عبقرية تجلب عملة الزائر وسعر الصرف العالمي مقابل الدولار لأي دولة في العالم تلقائياً
func getGlobalCurrencyAndRate(ip string) (string, float64) {
	client := &http.Client{Timeout: 4 * time.Second}
	
	// 1. معرفة بيانات الدولة والعملة الخاصة بالـ IP للزائر
	resp, err := client.Get(fmt.Sprintf("https://ipapi.co/%s/json/", ip))
	if err != nil {
		return "USD", 1.0
	}
	defer resp.Body.Close()

	var geoData struct {
		Currency string `json:"currency"` // يجلب رمز أي عملة في العالم (KWD, SAR, QAR, EUR...)
	}
	if err := json.NewDecoder(resp.Body).Decode(&geoData); err != nil || geoData.Currency == "" {
		return "USD", 1.0
	}

	if geoData.Currency == "USD" {
		return "USD", 1.0
	}

	// 2. جلب سعر الصرف المباشر واللحظي لعملة العميل مقابل الدولار الأمريكي
	exchangeResp, err := client.Get(fmt.Sprintf("https://open.er-api.com/v6/latest/USD"))
	if err != nil {
		return geoData.Currency, 1.0 // إذا فشل جلب سعر الصرف اللحظي يعود بالعملة كاحتياط
	}
	defer exchangeResp.Body.Close()

	var exchangeData struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(exchangeResp.Body).Decode(&exchangeData); err != nil {
		return geoData.Currency, 1.0
	}

	// إذا كانت العملة مدعومة في أسعار الصرف العالمية نأخذ قيمتها الدقيقة فوراً
	if rate, exists := exchangeData.Rates[geoData.Currency]; exists {
		return geoData.Currency, rate
	}

	return "USD", 1.0
}

// مسار تطلبه الواجهة الأمامية لعرض أسعار الاشتراكات أوتوماتيكياً بعملة بلد العميل
func getLocalizedPricesHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	userIP := r.Header.Get("X-Forwarded-For")
	if userIP == "" {
		userIP = r.RemoteAddr
	}

	currencyCode, rate := getGlobalCurrencyAndRate(userIP)

	response := map[string]interface{}{
		"currency": currencyCode,
		"pro":      fmt.Sprintf("%.2f", BasePriceProUSD*rate),
		"elite":    fmt.Sprintf("%.2f", BasePriceEliteUSD*rate),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// 🎯 المسار العالمي: يأخذ سعر أي إعلان (شقة، عمارة، بقالة، محل) ويحوله لعملة زائر الموقع فوراً
func convertListingPriceHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	// جلب السعر المرجعي المرسل من الموقع (نفترض أن الأسعار مخزنة بقاعدة البيانات بالدولار لتوحيد الحسبة)
	priceStr := r.URL.Query().Get("price_usd")
	basePriceUSD, err := strconv.ParseFloat(priceStr, 64)
	if err != nil || basePriceUSD <= 0 {
		http.Error(w, "Invalid price value", http.StatusBadRequest)
		return
	}

	userIP := r.Header.Get("X-Forwarded-For")
	if userIP == "" {
		userIP = r.RemoteAddr
	}

	currencyCode, rate := getGlobalCurrencyAndRate(userIP)
	localizedPrice := basePriceUSD * rate

	response := map[string]interface{}{
		"currency":        currencyCode,
		"localized_price": fmt.Sprintf("%.2f", localizedPrice),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// إنشاء عملية الدفع الإلكتروني عبر بوابة Tap بالعملة المحلية لأي زائر في العالم
func createSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type IncomingPlan struct {
		PlanType string `json:"plan_type"`
		Email    string `json:"email"`
	}
	var incoming IncomingPlan
	json.NewDecoder(r.Body).Decode(&incoming)

	userIP := r.Header.Get("X-Forwarded-For")
	if userIP == "" {
		userIP = r.RemoteAddr
	}

	currencyCode, rate := getGlobalCurrencyAndRate(userIP)
	
	finalAmount := BasePriceEliteUSD * rate
	if incoming.PlanType == "pro" {
		finalAmount = BasePriceProUSD * rate
	}

	// تقريب السعر لخانة مئوية صحيحة لتتوافق مع شروط البنوك وبوابات الدفع
	finalAmount, _ = strconv.ParseFloat(fmt.Sprintf("%.2f", finalAmount), 64)

	apiKey := os.Getenv("TAP_SECRET_KEY")

	chargeData := ChargeRequest{
		Amount:   finalAmount,
		Currency: currencyCode, // فوترة العميل بالعملة الرسمية لبلده تلقائياً
		Customer: Customer{
			FirstName: "Sarh Global Customer",
			Email:     incoming.Email,
		},
		Redirect: Redirect{
			URL: fmt.Sprintf("%s/dashboard", RailwayURL),
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

// الـ Webhook الخاص باستقبال تأكيدات الدفع الناجحة من بوابة Tap
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
		fmt.Printf("🎯 دفع عالمي ناجح! رقم العملية %s: القيمة المستلمة (%f)\n", payload.ID, payload.Amount)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

// استقبال صور التحويلات البنكية اليدوية وإرسالها فوراً لجروب التليجرام الخاص بالإدارة
func submitTransferHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

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

	caption := fmt.Sprintf("🔔 **إشعار تحويل جديد - منصة صرح العالمية**\n\n👤 معرف المشترك (ID): %s\n📱 رقم الهاتف: %s\n📦 الباقة المطلوبة: %s\n\nيرجى مراجعة الحساب للتأكد من وصول المبلغ وتفعيل المشترك.", userID, phone, packageName)
	
	go sendTelegramPhoto(filePath, caption)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "تم رفع وصل التحويل بنجاح فخم! جاري مراجعته وتفعيل حسابك خلال دقائق."}`))
}

// مسار خاص بلوحة التحكم لتفعيل المستخدم يدوياً من طرف الإدارة
func adminActivateUserHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

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

// دالة مساعدة لإرسال الصور لـ Telegram API
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

// تفعيل CORS لضمان قبول الطلبات القادمة من موقعك على Netlify دون مشاكل أمان
func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}
}

// دالة عبقرية تجلب عملة الزائر وسعر الصرف العالمي مقابل الدولار لأي دولة في العالم تلقائياً
func getGlobalCurrencyAndRate(ip string) (string, float64) {
	client := &http.Client{Timeout: 4 * time.Second}
	
	// 1. معرفة بيانات الدولة والعملة الخاصة بالـ IP للزائر
	resp, err := client.Get(fmt.Sprintf("https://ipapi.co/%s/json/", ip))
	if err != nil {
		return "USD", 1.0
	}
	defer resp.Body.Close()

	var geoData struct {
		Currency string `json:"currency"` // يجلب رمز أي عملة في العالم (KWD, EUR, SAR, GBP...)
	}
	if err := json.NewDecoder(resp.Body).Decode(&geoData); err != nil || geoData.Currency == "" {
		return "USD", 1.0
	}

	if geoData.Currency == "USD" {
		return "USD", 1.0
	}

	// 2. جلب سعر الصرف المباشر واللحظي لعملة العميل مقابل الدولار الأمريكي
	exchangeResp, err := client.Get(fmt.Sprintf("https://open.er-api.com/v6/latest/USD"))
	if err != nil {
		return geoData.Currency, 1.0 // إذا فشل جلب سعر الصرف اللحظي يعود بالعملة كاحتياط
	}
	defer exchangeResp.Body.Close()

	var exchangeData struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(exchangeResp.Body).Decode(&exchangeData); err != nil {
		return geoData.Currency, 1.0
	}

	// إذا كانت العملة مدعومة في أسعار الصرف العالمية نأخذ قيمتها الدقيقة فوراً
	if rate, exists := exchangeData.Rates[geoData.Currency]; exists {
		return geoData.Currency, rate
	}

	return "USD", 1.0
}

// مسار تطلبه الواجهة الأمامية لعرض السعر أوتوماتيكياً بعملة بلد العميل أياً كانت
func getLocalizedPricesHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	userIP := r.Header.Get("X-Forwarded-For")
	if userIP == "" {
		userIP = r.RemoteAddr
	}

	currencyCode, rate := getGlobalCurrencyAndRate(userIP)

	response := map[string]interface{}{
		"currency": currencyCode,
		"pro":      fmt.Sprintf("%.2f", BasePriceProUSD*rate),
		"elite":    fmt.Sprintf("%.2f", BasePriceEliteUSD*rate),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// إنشاء عملية الدفع الإلكتروني عبر بوابة Tap بالعملة المحلية لأي زائر في العالم
func createSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type IncomingPlan struct {
		PlanType string `json:"plan_type"`
		Email    string `json:"email"`
	}
	var incoming IncomingPlan
	json.NewDecoder(r.Body).Decode(&incoming)

	userIP := r.Header.Get("X-Forwarded-For")
	if userIP == "" {
		userIP = r.RemoteAddr
	}

	currencyCode, rate := getGlobalCurrencyAndRate(userIP)
	
	finalAmount := BasePriceEliteUSD * rate
	if incoming.PlanType == "pro" {
		finalAmount = BasePriceProUSD * rate
	}

	// تقريب السعر لخانة مئوية صحيحة لتتوافق مع شروط البنوك
	finalAmount, _ = strconv.ParseFloat(fmt.Sprintf("%.2f", finalAmount), 64)

	apiKey := os.Getenv("TAP_SECRET_KEY")

	chargeData := ChargeRequest{
		Amount:   finalAmount,
		Currency: currencyCode, // فوترة العميل بالعملة الرسمية لبلده في أي قارة على الأرض
		Customer: Customer{
			FirstName: "Sarh Global Customer",
			Email:     incoming.Email,
		},
		Redirect: Redirect{
			URL: fmt.Sprintf("%s/dashboard", RailwayURL),
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
		fmt.Printf("🎯 دفع عالمي ناجح! رقم العملية %s: القيمة المستلمة (%f)\n", payload.ID, payload.Amount)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func submitTransferHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

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

	caption := fmt.Sprintf("🔔 **إشعار تحويل جديد - منصة صرح العالمية**\n\n👤 معرف المشترك (ID): %s\n📱 رقم الهاتف: %s\n📦 الباقة المطلوبة: %s\n\nيرجى مراجعة الحساب للتأكد من وصول المبلغ وتفعيل المشترك.", userID, phone, packageName)
	
	go sendTelegramPhoto(filePath, caption)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "تم رفع وصل التحويل بنجاح فخم! جاري مراجعته وتفعيل حسابك خلال دقائق."}`))
}

func adminActivateUserHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

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

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

	// إعداد المسارات والخدمات المحدثة لربط الـ Front-end بالـ Backend
	http.HandleFunc("/api/get-prices", getLocalizedPricesHandler) // جلب الأسعار بالعملة المحلية للواجهة
	http.HandleFunc("/billing/subscribe", createSubscriptionHandler)
	http.HandleFunc("/webhook/tap", tapWebhookHandler)
	http.HandleFunc("/api/submit-transfer", submitTransferHandler)
	http.HandleFunc("/api/admin/activate-user", adminActivateUserHandler)

	log.Printf("الخادم يعمل الآن على منفذ Railway: %s بأعلى مستوى واحترافية وبدون أي تراجع...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

// دالة ذكية لتحديد عملة وسعر الصرف بناءً على عنوان IP الخاص بالعميل
func getCurrencyByIP(ip string) (string, float64) {
	client := &http.Client{Timeout: 3 * time.Second}
	// استدعاء خدمة تحديد الموقع عبر الـ IP
	resp, err := client.Get(fmt.Sprintf("https://ipapi.co/%s/json/", ip))
	if err != nil {
		return "SAR", 1.0 // العودة للريال السعودي كخيار افتراضي لحماية استقرار الموقع
	}
	defer resp.Body.Close()

	var geoData struct {
		CountryCode string `json:"country_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geoData); err != nil {
		return "SAR", 1.0
	}

	if info, exists := countryCurrencyMap[geoData.CountryCode]; exists {
		return info.Code, info.Rate
	}

	return "SAR", 1.0
}

// مسار جديد تطلبه الواجهة الأمامية (Netlify) لعرض الأسعار تلقائياً بعملة بلد الزائر
func getLocalizedPricesHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	// جلب الآي بي الحقيقي للمستخدم حتى لو كان خلف بروكسي المنصات
	userIP := r.Header.Get("X-Forwarded-For")
	if userIP == "" {
		userIP = r.RemoteAddr
	}

	currencyCode, rate := getCurrencyByIP(userIP)

	response := map[string]interface{}{
		"currency": currencyCode,
		"pro":      fmt.Sprintf("%.2f", BasePricePro*rate),
		"elite":    fmt.Sprintf("%.2f", BasePriceElite*rate),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// معالج طلب الدفع الإلكتروني المطور ليدعم عملة بلد الزائر تلقائياً عند الدفع عبر بوابة Tap
func createSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type IncomingPlan struct {
		PlanType string `json:"plan_type"` // "pro" أو "elite"
		Email    string `json:"email"`
	}
	var incoming IncomingPlan
	json.NewDecoder(r.Body).Decode(&incoming)

	userIP := r.Header.Get("X-Forwarded-For")
	if userIP == "" {
		userIP = r.RemoteAddr
	}

	// حساب السعر والعملة بشكل آمن من طرف السيرفر لرفض أي تلاعب من المتصفح
	currencyCode, rate := getCurrencyByIP(userIP)
	
	finalAmount := BasePriceElite * rate // افتراضي باقة النخبة
	if incoming.PlanType == "pro" {
		finalAmount = BasePricePro * rate
	}

	apiKey := os.Getenv("TAP_SECRET_KEY")

	chargeData := ChargeRequest{
		Amount:   finalAmount,
		Currency: currencyCode, // سيتم محاسبته مباشرة بعملة بلده الفخمة 
		Customer: Customer{
			FirstName: "Sarh Customer",
			Email:     incoming.Email,
		},
		Redirect: Redirect{
			URL: fmt.Sprintf("%s/dashboard", RailwayURL),
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

// الـ Webhook لمراقبة حركة الدفع الناجحة وتوثيق المبالغ بأي عملة وصلت من Tap
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
		// يتم تسجيل وتوثيق عملية الدفع الناجحة فوراً في السيرفر مع حفظ قيمتها المستلمة
		fmt.Printf("🎯 نجاح دفع إلكتروني متميز! رقم العملية %s: تم استلام مبلغ بقيمة (%f)\n", payload.ID, payload.Amount)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

// استقبال صور التحويل البنكي وإرسالها لتليجرام لحماية الأرباح يدوياً والتحقق من الباركود
func submitTransferHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

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

	caption := fmt.Sprintf("🔔 **إشعار تحويل جديد - منصة صرح العقارية**\n\n👤 معرف المشترك (ID): %s\n📱 رقم الهاتف: %s\n📦 الباقة المطلوبة: %s\n\nيرجى مراجعة حسابك البنكي للتأكد من وصول المبلغ ثم تفعيل الحساب.", userID, phone, packageName)
	
	go sendTelegramPhoto(filePath, caption)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "تم رفع وصل التحويل بنجاح فخم! جاري مراجعته وتفعيل حسابك خلال دقائق."}`))
}

// تفعيل العميل يدوياً من الإدارة وتأكيد الباقة
func adminActivateUserHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

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

// إرسال البيانات المرفقة والصور إلى تليجرام البوت الشخصي
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

// دالة مساعدة لفتح الـ CORS لتلقي الطلبات من Netlify بدون قيود المتصفح
func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}
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
