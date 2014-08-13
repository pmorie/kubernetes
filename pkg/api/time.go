package api

import (
	"encoding/json"
	"fmt"
	"time"
)

func (t *Time) UnmarshalJSON(b []byte) error {
	fmt.Println("KubeTime.UnmarshalJSON")

	var str string
	json.Unmarshal(b, &str)

	pt, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return err
	}

	t.Time = pt
	return nil
}

// MarshalJSON implements the json.Marshaler interface.
func (t Time) MarshalJSON() ([]byte, error) {
	fmt.Println("KubeTime.MarshalJSON")
	return json.Marshal(t.Format(time.RFC3339))
}

func (t *Time) SetYAML(tag string, value interface{}) bool {
	fmt.Println("KubeTime.SetYAML")
	str, ok := value.(string)
	if !ok {
		return false
	}

	pt, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return false
	}

	t.Time = pt
	return true
}

func (t Time) GetYAML() (tag string, value interface{}) {
	fmt.Println("KubeTime.GetYAML")
	value = t.Format(time.RFC3339)
	return tag, value
}

func Now() Time {
	return Time{time.Now()}
}
