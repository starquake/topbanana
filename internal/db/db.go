package db

import (
	"database/sql/driver"
	"fmt"
	"time"
)

type Timestamp time.Time

func (t *Timestamp) Scan(value interface{}) error {
	if value == nil {
		*t = Timestamp(time.Time{})

		return nil
	}

	ms, ok := value.(int64)
	if !ok {
		return fmt.Errorf("cannot scan %T into Timestamp", value)
	}

	*t = Timestamp(time.UnixMilli(ms))

	return nil
}

func (t Timestamp) Value() (driver.Value, error) {
	return time.Time(t).UnixMilli(), nil
}
