package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/avct/uasurfer"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/firehose"
	"github.com/joncalhoun/qson"
	"github.com/pariz/gountries"
	uuid "github.com/satori/go.uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/tomasen/realip"
	"github.com/ua-parser/uap-go/uaparser"
)

// Exp cookie
const (
	CidExp = 24 * 90 * time.Hour
	SidExp = 30 * time.Hour
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
func enableCors(w *http.ResponseWriter, req *http.Request) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
	(*w).Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT")
	(*w).Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
}

const transPixel = "\x47\x49\x46\x38\x39\x61\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\x00\x00\x00\x21\xF9\x04\x01\x00\x00\x00\x00\x2C\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02\x44\x01\x00\x3B"

func pixelWriter(w http.ResponseWriter) {
	// Pixel
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.Header().Set("Expires", "Wed, 11 Nov 1998 11:11:11 GMT")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, post-check=0, pre-check=0")
	w.Header().Set("Pragma", "no-cache")
	fmt.Fprintf(w, transPixel)
}
func getCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func initCookieIfNeed(w http.ResponseWriter, r *http.Request, body string) (string, error) {
	var err error
	clientID := gjson.Get(body, "clientId").String()
	sessionID := gjson.Get(body, "sessionId").String()

	if clientID == "" {
		clientID = getCookie(r, "__cfje_cid")
		if clientID == "" {
			u1, err := uuid.NewV4()
			if err == nil {
				clientID = u1.String()
				cookie := http.Cookie{Name: "__cfje_cid", Value: clientID, Expires: time.Now().Add(CidExp)}
				http.SetCookie(w, &cookie)
			}
		}
		if clientID != "" {
			body, err = sjson.Set(body, "clientId", clientID)
		}
	}
	if sessionID == "" {
		sessionID = getCookie(r, "__cfje_sid")
		if sessionID == "" {
			u2, err := uuid.NewV4()
			if err == nil {
				sessionID = u2.String()
				cookie := http.Cookie{Name: "__cfje_sid", Value: sessionID, Expires: time.Now().Add(SidExp)}
				http.SetCookie(w, &cookie)
			}
		}
		if sessionID != "" {
			body, err = sjson.Set(body, "sessionId", clientID)
		}
	}
	return body, err
}

var (
	streamName = getEnv("STREAM_NAME", "")
	region     = "ap-northeast-1"
)
var parser, _ = uaparser.New("./regexes.yaml")
var gountriesQuery = gountries.New()

var maxBatchSize, _ = strconv.Atoi(getEnv("BATCH_SIZE", "200"))
var maxTime int64 = 5 * 60 * 1000

// Memory batch variables
var records = []*firehose.Record{}
var startTime = time.Now().UnixNano() / 1e6

// PutRecordBatch to push data to firehouse, golang variable safe with concurrent
func PutRecordBatch(line string) error {
	records = append(
		records,
		&firehose.Record{Data: []byte(line)},
	)
	timeDistance := time.Now().UnixNano()/1e6 - startTime
	if len(records) < maxBatchSize && timeDistance < maxTime {
		return nil
	}
	processRecods := records                //tmp var
	records = []*firehose.Record{}          //Empty data
	startTime = time.Now().UnixNano() / 1e6 // Reset time

	// Create a Firehose client with additional configuration

	sess := session.Must(session.NewSession())
	firehoseService := firehose.New(sess, aws.NewConfig().WithRegion(region))
	recordsBatchInput := &firehose.PutRecordBatchInput{}
	recordsBatchInput = recordsBatchInput.SetDeliveryStreamName(streamName)
	recordsBatchInput = recordsBatchInput.SetRecords(processRecods)
	_, err := firehoseService.PutRecordBatch(recordsBatchInput)
	if err != nil { // Restore faild record
		records = append(
			records,
			processRecods...,
		)
	}
	return err
}

func handleTracking(w http.ResponseWriter, r *http.Request) {
	enableCors(&w, r)
	var shouldRespImg bool
	var body string
	var err error
	switch r.Method {
	case http.MethodGet:
		// Parse params
		shouldRespImg = true
		b, err := qson.ToJSON(r.URL.RawQuery)
		if err == nil {
			body = string(b)
		}
	case http.MethodPost:
		b, err := ioutil.ReadAll(r.Body)
		if err == nil {
			body = string(b)
		}
	case http.MethodOptions:
		w.WriteHeader(http.StatusAccepted)
		return
	default:
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}
	ua := r.Header.Get("User-Agent")
	client := parser.Parse(ua)
	uaDevice := uasurfer.Parse(ua)
	body, err = sjson.Set(body, "ip", realip.FromRequest(r))
	body, err = sjson.Set(body, "device", client.Device.Family)
	body, err = sjson.Set(body, "deviceType", uaDevice.DeviceType.String())
	body, err = sjson.Set(body, "browser", client.UserAgent.ToString())
	body, err = sjson.Set(body, "os", client.Os.ToString())
	body, err = sjson.Set(body, "ua", ua)
	body, err = sjson.Set(body, "createdAt", time.Now().UTC())
	body, err = sjson.Set(body, "countryCode", r.Header.Get("CloudFront-Viewer-Country")) //Use cloudfront
	body, err = initCookieIfNeed(w, r, body)
	country, cErr := gountriesQuery.FindCountryByAlpha(r.Header.Get("CloudFront-Viewer-Country"))
	if cErr == nil && country.Name.Common != "" {
		body, err = sjson.Set(body, "country", country.Name.Common)
	}
	if err == nil {
		err = PutRecordBatch(body + "\n")
	}
	if err == nil {
		w.WriteHeader(http.StatusOK)
		if shouldRespImg == true {
			pixelWriter(w)
		}
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	http.HandleFunc("/_healthcheck", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	http.HandleFunc("/api/pixel", handleTracking)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
