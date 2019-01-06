package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
)

var (
	hueIP     string
	hueUserID string
	lightID   string

	hueTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	hueClient = &http.Client{Transport: hueTransport}
)

func init() {
	flag.StringVar(&hueIP, "hueip", "192.168.86.64", "Hue bridge IP address")
	flag.StringVar(&hueUserID, "hueuser", "", "Hue user ID")
	flag.StringVar(&lightID, "light", "6", "Hue light ID")
}

func notifyHR(sensorID string, hr byte) error {

	type state struct {
		On  bool   `json:"on"`
		Bri uint8  `json:"bri"`
		Hue uint16 `json:"hue"`
		Sat uint8  `json:"sat"`
	}

	const (
		baseHR = 70
		maxHR  = 170
	)

	// Hue 65535 should be red. Saturation goes from 0 to 255. 0 will be white. So light gets redder as HR
	// goes up.
	// Will be completely white at 70 or below, and completely red at 170 and above
	sat := (int(hr) - baseHR) * 255 / (maxHR - baseHR)
	if sat < 0 {
		sat = 0
	} else if sat > 255 {
		sat = 255
	}

	return hueUpdate("lights", lightID, "state", state{
		On:  true,
		Bri: 255,
		Hue: 65535,
		Sat: uint8(sat),
	})
}

// setSensor sets a HR sensor in the Hue bridge to the HR value. Idea was to create rules that act on this
// value. Have dropped that for now.
func setSensor(sensorID string, hr byte) error {
	return hueUpdate("sensors", sensorID, "state", sensorState{
		Status: int(hr),
	})
}

type sensorState struct {
	Status int `json:"status"`
}

type sensorConfig struct {
	On        bool  `json:"on,omitempty"`
	Reachable bool  `json:"reachable,omitempty"`
	Battery   uint8 `json:"battery,omitempty"`
}

type sensor struct {
	Name             string        `json:"name"`
	ModelID          string        `json:"modelid"`
	SWVersion        string        `json:"swversion"`
	Type             string        `json:"type"`
	UniqueID         string        `json:"uniqueid"`
	ManufacturerName string        `json:"manufacturername"`
	State            *sensorState  `json:"state,omitempty"`
	Config           *sensorConfig `json:"config,omitempty"`
	Recycle          bool          `json:"recycle,omitempty"`
}

type idResponse struct {
	ID string `json:"id"`
}

type errorDescription struct {
	Type        int    `json:"type,omitempty"`
	Address     string `json:"address,omitempty"`
	Description string `json:"description,omitempty"`
}

type response struct {
	Success idResponse       `json:"success"`
	Error   errorDescription `json:"error"`
}

var sensorDef = sensor{
	Name:             "Phil's heart rate",
	Type:             "CLIPGenericStatus",
	ModelID:          "model1",
	ManufacturerName: "Phil Custom Sensors",
	SWVersion:        "v0.0.1",
	UniqueID:         "lalalalala",
}

func ensureSensor() (id string, err error) {
	sensors, err := getSensors()
	if err != nil {
		return "", err
	}

	for id, sensor := range sensors {
		// May in future want to install a sensor per person
		if sensor.ManufacturerName == sensorDef.ManufacturerName &&
			sensor.UniqueID == sensorDef.UniqueID {
			return id, nil
		}
	}

	return createSensor()
}

func getSensors() (map[string]sensor, error) {
	payload := make(map[string]sensor)
	err := hueList("sensors", &payload)
	return payload, err
}

func createSensor() (id string, err error) {
	return hueCreate("sensors", sensorDef)
}

type condition struct {
	Address  string
	Operator string
	Value    string
}

type action struct {
	Address string
	Method  string
	Body    json.RawMessage
}

type rule struct {
	Name       string
	Owner      string
	Conditions []condition
	Actions    []action
}

// TODO: need some complicated rules!

func hueUpdate(entityType, id, endpoint string, payload interface{}) error {
	url := fmt.Sprintf("https://%s/api/%s/%s/%s/%s", hueIP, hueUserID, entityType, id, endpoint)

	data, err := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(data))
	if err != nil {
		return err
	}

	rsp, err := hueClient.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()

	data, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		return err
	}

	if rsp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s (%s)", rsp.Status, string(data))
	}

	return nil

}

func hueList(entityType string, payload interface{}) error {
	url := fmt.Sprintf("https://%s/api/%s/%s", hueIP, hueUserID, entityType)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	rsp, err := hueClient.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()

	data, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return err
	}

	if rsp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s (%s)", rsp.Status, string(data))
	}

	return json.Unmarshal(data, payload)
}

func hueCreate(entityType string, body interface{}) (id string, err error) {
	url := fmt.Sprintf("https://%s/api/%s/%s", hueIP, hueUserID, entityType)

	data, err := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}

	rsp, err := hueClient.Do(req)
	if err != nil {
		return "", err
	}
	defer rsp.Body.Close()

	data, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		return "", err
	}

	if rsp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status %s (%s)", rsp.Status, string(data))
	}

	var payload []response
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}

	// todo process errors in response

	return payload[0].Success.ID, nil

}
