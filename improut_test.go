package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLutimInfo(t *testing.T) {
	req, err := http.NewRequest("GET", "/infos", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(lutimInfo)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("/infos returned non-OK: %v", status)
	}

	reply := rr.Body.String()
	if !strings.Contains(reply, `"max_file_size":10485760`) {
		t.Errorf("/infos should contain max_file_size")
	}
}

func TestUpload(t *testing.T) {
	dir, err := ioutil.TempDir(".", "tmpstorage")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	*storageRoot = dir

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "joconde.png")
	if err != nil {
		t.Fatal(err)
	}
	dummyImageBytes := []byte{1, 2, 3, 42}
	if _, err := part.Write(dummyImageBytes); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	req, err := http.NewRequest("POST", "/", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(dispatch)
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusFound {
		t.Errorf("expected StatusFound, got %v", status)
	}
	deletionToken := rr.Header().Get(kDeletionTokenHeader)
	if deletionToken == "" {
		t.Error("empty deletion token")
	}
	location := rr.Header().Get("Location")
	if location != "/3677e35be4b1ad2d.png" {
		t.Errorf("wrong Location header: %v", location)
	}

	req, err = http.NewRequest("GET", location, nil)
	if err != nil {
		log.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("expected StatusOK, got %v", status)
	}
	data, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		log.Fatal(err)
	}
	if !reflect.DeepEqual(data, dummyImageBytes) {
		t.Errorf("wrong bytes, got %+v", data)
	}

	req, err = http.NewRequest("DELETE", location, nil)
	if err != nil {
		log.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("expected StatusNotFound, got %v", status)
	}

	req, err = http.NewRequest("DELETE", location, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set(kDeletionTokenHeader, deletionToken)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusNoContent {
		t.Errorf("expected StatusNoContent, got %v", status)
	}

	req, err = http.NewRequest("GET", location, nil)
	if err != nil {
		log.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("expected StatusNotFound, got %v", status)
	}
}

func TestLutimUpload(t *testing.T) {
	dir, err := ioutil.TempDir(".", "tmpstorage")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	*storageRoot = dir

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "joconde.png")
	if err != nil {
		t.Fatal(err)
	}
	dummyImageBytes := []byte{1, 2, 3, 42}
	if _, err := part.Write(dummyImageBytes); err != nil {
		t.Fatal(err)
	}
	writer.WriteField("format", "json")
	writer.WriteField("delete-day", "42")
	writer.Close()
	req, err := http.NewRequest("POST", "/", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(dispatch)
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("expected StatusOK, got %v", status)
	}
	var lr lutimUploadReply
	if err := json.Unmarshal(rr.Body.Bytes(), &lr); err != nil {
		t.Fatal(err)
	}
	if !lr.Success {
		t.Errorf("success is not true")
	}
	if lr.Message.Short != "3677e35be4b1ad2d.png" {
		t.Errorf("wrong short: %s", lr.Message.Short)
	}
	if lr.Message.LifetimeDays != 42 {
		t.Errorf("wrong lifetime %d", lr.Message.LifetimeDays)
	}
	if lr.Message.Filename != "joconde.png" {
		t.Errorf("wrong filename %s", lr.Message.Filename)
	}
	if lr.Message.Token == "" {
		t.Errorf("empty deletion token")
	}

	url := "/3677e35be4b1ad2d.png"
	req, err = http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("expected StatusOK, got %v", status)
	}
	data, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		log.Fatal(err)
	}
	if !reflect.DeepEqual(data, dummyImageBytes) {
		t.Errorf("wrong bytes, got %+v", data)
	}

	req, err = http.NewRequest("GET", "/d/"+lr.Message.Short+"/bad", nil)
	if err != nil {
		log.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("expected StatusNotFound, got %v", status)
	}

	req, err = http.NewRequest("GET", "/d/"+lr.Message.Short+"/"+lr.Message.Token, nil)
	if err != nil {
		log.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("expected StatusOK, got %v", status)
	}

	req, err = http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("expected StatusNotFound, got %v", status)
	}
}
