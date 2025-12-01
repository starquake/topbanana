package quiz_test

import (
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

func TestTimestamp_Scan(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   any
		want    quiz.Timestamp
		wantErr bool
	}{
		{
			name:    "valid timestamp",
			value:   int64(1764575476147),
			want:    quiz.Timestamp(time.Unix(1764575476, 147*int64(time.Millisecond))),
			wantErr: false,
		},
		{
			name:    "invalid timestamp",
			value:   "invalid",
			want:    quiz.Timestamp{},
			wantErr: true,
		},
		{
			name:    "nil timestamp",
			value:   nil,
			want:    quiz.Timestamp{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var ts quiz.Timestamp
			err := ts.Scan(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("Scan() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTimestamp_Value(t *testing.T) {
	t.Parallel()
	ts := quiz.Timestamp(time.Unix(1764575476, 147*int64(time.Millisecond)))
	got, err := ts.Value()
	if err != nil {
		t.Errorf("Value() error = %v", err)
	}
	want := int64(1764575476147)
	if got != want {
		t.Errorf("Value() = %v, want %v", got, want)
	}
}
