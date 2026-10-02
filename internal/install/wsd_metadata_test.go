package install

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

const metadataTestDeviceID = "urn:uuid:e3248000-80ce-11db-8000-30c9ab962e73"

func metadataTestResponse(messageID, action, serviceID, hostedAddress string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:x="http://schemas.xmlsoap.org/ws/2004/09/mex" xmlns:d="http://schemas.xmlsoap.org/ws/2006/02/devprof">
<s:Header><a:Action>%s</a:Action><a:RelatesTo>%s</a:RelatesTo></s:Header>
<s:Body><x:Metadata><x:MetadataSection Dialect="http://schemas.xmlsoap.org/ws/2006/02/devprof/Relationship"><d:Relationship Type="http://schemas.xmlsoap.org/ws/2006/02/devprof/host"><d:Hosted><a:EndpointReference><a:Address>%s</a:Address></a:EndpointReference><d:ServiceId>%s</d:ServiceId></d:Hosted></d:Relationship></x:MetadataSection></x:Metadata></s:Body></s:Envelope>`, action, messageID, hostedAddress, serviceID))
}

func TestValidateWSDMetadataResponse(t *testing.T) {
	location, err := url.Parse("http://192.0.2.25:80/WebServices/Device")
	if err != nil {
		t.Fatal(err)
	}
	messageID := "urn:uuid:71d9b306-2b4d-43d8-8047-9e0c64fb0d4c"
	serviceID := "uri:e3248000-80ce-11db-8000-30c9ab962e73/PrinterService"
	endpoint := "http://192.0.2.25:80/WebServices/PrinterService"
	for _, test := range []struct {
		name, relatesTo, action, serviceID, endpoint string
		wantError                                    bool
	}{
		{"matching Brother metadata", messageID, wsdTransferGetResponseAction, serviceID, endpoint, false},
		{"wrong request", "urn:uuid:99c42146-01fb-4d8c-8266-ea16f5c2b004", wsdTransferGetResponseAction, serviceID, endpoint, true},
		{"wrong SOAP action", messageID, "http://schemas.xmlsoap.org/ws/2004/09/transfer/Get", serviceID, endpoint, true},
		{"wrong device", messageID, wsdTransferGetResponseAction, "uri:00000000-0000-4000-8000-000000000000/PrinterService", endpoint, true},
		{"wrong service host", messageID, wsdTransferGetResponseAction, serviceID, "http://192.0.2.26/PrinterService", true},
		{"no hosted service", messageID, wsdTransferGetResponseAction, serviceID, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := metadataTestResponse(test.relatesTo, test.action, test.serviceID, test.endpoint)
			err := validateWSDMetadataResponse(body, location, metadataTestDeviceID, messageID)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

func TestValidateWSDMetadataResponseWithRootEndpoint(t *testing.T) {
	location, err := url.Parse("http://192.0.2.25/WebServices/Device")
	if err != nil {
		t.Fatal(err)
	}
	messageID := "urn:uuid:71d9b306-2b4d-43d8-8047-9e0c64fb0d4c"
	response := string(metadataTestResponse(messageID, wsdTransferGetResponseAction, "https://example.test/opaque-service-id", "http://192.0.2.25/PrinterService"))
	response = strings.Replace(response, "<d:Hosted>", "<d:Host><a:EndpointReference><a:Address>"+metadataTestDeviceID+"</a:Address></a:EndpointReference></d:Host><d:Hosted>", 1)
	if err := validateWSDMetadataResponse([]byte(response), location, metadataTestDeviceID, messageID); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWSDMetadataHTTP(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || !strings.HasPrefix(request.Header.Get("Content-Type"), "application/soap+xml") {
			t.Errorf("unexpected WSD request: %s %s", request.Method, request.Header.Get("Content-Type"))
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var envelope struct {
			Header struct {
				To        string `xml:"To"`
				Action    string `xml:"Action"`
				MessageID string `xml:"MessageID"`
			} `xml:"Header"`
		}
		if err := xml.Unmarshal(body, &envelope); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if envelope.Header.To != metadataTestDeviceID || envelope.Header.Action != wsdTransferGetAction || envelope.Header.MessageID == "" {
			t.Errorf("unexpected WSD SOAP header: %+v", envelope.Header)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		_, _ = writer.Write(metadataTestResponse(envelope.Header.MessageID, wsdTransferGetResponseAction, "uri:e3248000-80ce-11db-8000-30c9ab962e73/PrinterService", server.URL+"/PrinterService"))
	}))
	defer server.Close()
	if err := verifyWSDMetadata(context.Background(), server.URL+"/Device", metadataTestDeviceID); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWSDMetadataRejectsRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("WSD metadata verifier followed a redirect")
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", target.URL)
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	if err := verifyWSDMetadata(context.Background(), source.URL, metadataTestDeviceID); err == nil {
		t.Fatal("redirected WSD metadata was accepted")
	}
}

// This optional probe uses the already installed WSD printer; it does not
// create queues or send a print job.
func TestHardwareWSDMetadata(t *testing.T) {
	location := os.Getenv("SPOOLSMITH_TEST_WSD_METADATA_URL")
	deviceID := os.Getenv("SPOOLSMITH_TEST_WSD_DEVICE_ID")
	if location == "" || deviceID == "" {
		t.Skip("set SPOOLSMITH_TEST_WSD_METADATA_URL and SPOOLSMITH_TEST_WSD_DEVICE_ID")
	}
	if err := verifyWSDMetadata(context.Background(), location, deviceID); err != nil {
		t.Fatal(err)
	}
}
