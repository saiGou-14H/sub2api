// SPDX-License-Identifier: Apache-2.0
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// decodeJob supplies only Rust serde defaults; it does not run business validation.
func decodeJob(b []byte, out any) error {
	v := reflect.ValueOf(out).Elem()
	v.SetZero()
	switch v.Type().Name() {
	case "jobStream":
		v.FieldByName("FirstRetainedLine").SetUint(1)
		v.FieldByName("NextLine").SetUint(1)
	case "jobInfo":
		v.FieldByName("Kind").SetString("shell")
	case "jobSnapshot":
		for _, n := range []string{"Stdout", "Stderr"} {
			v.FieldByName(n).Set(reflect.ValueOf(ShellJobStreamSnapshot{FirstRetainedLine: 1, NextLine: 1}))
		}
	}
	if trimmed := bytes.TrimSpace(b); len(trimmed) > 0 && trimmed[0] == '[' {
		var err error
		b, err = jobSequenceObject(b, v.Type().Name())
		if err != nil {
			return err
		}
	}
	return decodeObjectFields(b, out, v.Type().Name() == "jobActivity", decodeJobField)
}
func marshalJob(v any) ([]byte, error) {
	x := reflect.New(reflect.TypeOf(v)).Elem()
	x.Set(reflect.ValueOf(v))
	for i := 0; i < x.NumField(); i++ {
		f := x.Field(i)
		if f.Kind() == reflect.Slice && f.IsNil() {
			f.Set(reflect.MakeSlice(f.Type(), 0, 0))
		}
	}
	return json.Marshal(x.Interface())
}

type jobUpdate RunnerJobUpdateRequest

func (x *RunnerJobUpdateRequest) UnmarshalJSON(b []byte) error {
	var v jobUpdate
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobUpdateRequest(v)
	return nil
}

type jobUpdateResponse RunnerJobUpdateResponse

func (x *RunnerJobUpdateResponse) UnmarshalJSON(b []byte) error {
	var v jobUpdateResponse
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobUpdateResponse(v)
	return nil
}

type jobCodex ShellJobCodexMetadata

func (x *ShellJobCodexMetadata) UnmarshalJSON(b []byte) error {
	var v jobCodex
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobCodexMetadata(v)
	return nil
}

type jobStep ShellJobValidationStep

func (x *ShellJobValidationStep) UnmarshalJSON(b []byte) error {
	var v jobStep
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobValidationStep(v)
	return nil
}
func (x ShellJobValidationStep) MarshalJSON() ([]byte, error) { return marshalJob(jobStep(x)) }

type jobProgress ShellJobValidationProgress

func (x *ShellJobValidationProgress) UnmarshalJSON(b []byte) error {
	var v jobProgress
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobValidationProgress(v)
	return nil
}

type jobActivity ShellJobActivity

func (x *ShellJobActivity) UnmarshalJSON(b []byte) error {
	var v jobActivity
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobActivity(v)
	return nil
}

type jobValidation ShellJobValidationMetadata

func (x *ShellJobValidationMetadata) UnmarshalJSON(b []byte) error {
	var v jobValidation
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobValidationMetadata(v)
	return nil
}
func (x ShellJobValidationMetadata) MarshalJSON() ([]byte, error) {
	return marshalJob(jobValidation(x))
}

type jobStructured ShellJobStructuredExecutionMetadata

func (x *ShellJobStructuredExecutionMetadata) UnmarshalJSON(b []byte) error {
	var v jobStructured
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobStructuredExecutionMetadata(v)
	return nil
}

type jobContext ShellJobContext

func (x *ShellJobContext) UnmarshalJSON(b []byte) error {
	var v jobContext
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobContext(v)
	return nil
}

type jobStream ShellJobStreamSnapshot

func (x *ShellJobStreamSnapshot) UnmarshalJSON(b []byte) error {
	var v jobStream
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobStreamSnapshot(v)
	return nil
}

type jobLogSnapshot ShellJobLogSnapshot

func (x *ShellJobLogSnapshot) UnmarshalJSON(b []byte) error {
	var v jobLogSnapshot
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobLogSnapshot(v)
	return nil
}

type jobSnapshot ShellJobSnapshot

func (x *ShellJobSnapshot) UnmarshalJSON(b []byte) error {
	var v jobSnapshot
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobSnapshot(v)
	return nil
}

type jobInventory ShellJobInventory

func (x *ShellJobInventory) UnmarshalJSON(b []byte) error {
	var v jobInventory
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobInventory(v)
	return nil
}
func (x ShellJobInventory) MarshalJSON() ([]byte, error) { return marshalJob(jobInventory(x)) }

type jobShellResult RunnerShellJobResult

func (x *RunnerShellJobResult) UnmarshalJSON(b []byte) error {
	var v jobShellResult
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerShellJobResult(v)
	return nil
}

type jobResult RunnerJobResult

func (x *RunnerJobResult) UnmarshalJSON(b []byte) error {
	var v jobResult
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobResult(v)
	return nil
}

type jobInfo ShellJobInfo

func (x *ShellJobInfo) UnmarshalJSON(b []byte) error {
	var v jobInfo
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = ShellJobInfo(v)
	return nil
}

type jobStatusRequest RunnerJobStatusRequest

func (x *RunnerJobStatusRequest) UnmarshalJSON(b []byte) error {
	var v jobStatusRequest
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobStatusRequest(v)
	return nil
}

type jobStopRequest RunnerJobStopRequest

func (x *RunnerJobStopRequest) UnmarshalJSON(b []byte) error {
	var v jobStopRequest
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobStopRequest(v)
	return nil
}

type jobLogRequest RunnerJobLogRequest

func (x *RunnerJobLogRequest) UnmarshalJSON(b []byte) error {
	var v jobLogRequest
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobLogRequest(v)
	return nil
}

type jobListRequest RunnerJobsListRequest

func (x *RunnerJobsListRequest) UnmarshalJSON(b []byte) error {
	var v jobListRequest
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobsListRequest(v)
	return nil
}

type jobStatusResponse RunnerJobStatusResponse

func (x *RunnerJobStatusResponse) UnmarshalJSON(b []byte) error {
	var v jobStatusResponse
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobStatusResponse(v)
	return nil
}

type jobStopResponse RunnerJobStopResponse

func (x *RunnerJobStopResponse) UnmarshalJSON(b []byte) error {
	var v jobStopResponse
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobStopResponse(v)
	return nil
}

type jobLogResponse RunnerJobLogResponse

func (x *RunnerJobLogResponse) UnmarshalJSON(b []byte) error {
	var v jobLogResponse
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobLogResponse(v)
	return nil
}

type jobListResponse RunnerJobsListResponse

func (x *RunnerJobsListResponse) UnmarshalJSON(b []byte) error {
	var v jobListResponse
	if e := decodeJob(b, &v); e != nil {
		return e
	}
	*x = RunnerJobsListResponse(v)
	return nil
}
func (x RunnerJobsListResponse) MarshalJSON() ([]byte, error) { return marshalJob(jobListResponse(x)) }

func (x *ShellJobEnvPair) UnmarshalJSON(b []byte) error {
	var a []json.RawMessage
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	if len(a) != 2 {
		return fmt.Errorf("env tuple requires exactly two strings")
	}
	var p ShellJobEnvPair
	for i := range a {
		if bytes.Equal(bytes.TrimSpace(a[i]), []byte("null")) {
			return fmt.Errorf("env tuple cannot contain null")
		}
		if err := json.Unmarshal(a[i], &p[i]); err != nil {
			return err
		}
	}
	*x = p
	return nil
}
func jobEnum[T ~string](b []byte, out *T, values ...T) error {
	if err := validateJSONUnicode(b); err != nil {
		return err
	}
	b = bytes.TrimSpace(b)
	var s string
	if len(b) > 0 && b[0] == '{' {
		// Token decoding retains duplicate variant keys. A map would lose them.
		if !json.Valid(b) {
			return fmt.Errorf("invalid Job unit enum JSON")
		}
		d := json.NewDecoder(bytes.NewReader(b))
		_, _ = d.Token()
		if !d.More() {
			return fmt.Errorf("Job unit enum requires exactly one variant")
		}
		key, err := d.Token()
		if err != nil {
			return err
		}
		s = key.(string)
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return err
		}
		if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) || d.More() {
			return fmt.Errorf("Job unit enum requires exactly one variant mapped to null")
		}
	} else if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	for _, v := range values {
		if string(v) == s {
			*out = v
			return nil
		}
	}
	return fmt.Errorf("unknown Job enum value %q", s)
}
func (x *ShellJobActivityState) UnmarshalJSON(b []byte) error {
	return jobEnum(b, x, ActivityWorking, ActivityWaiting)
}
func (x *ShellJobActivityPhase) UnmarshalJSON(b []byte) error {
	return jobEnum(b, x, ActivityProcessRunning, ActivityValidationFormat, ActivityValidationCheck, ActivityValidationTest, ActivityCargoWaitingForBuildLock, ActivityCargoCompiling, ActivityCargoChecking)
}
func (x *ShellJobActivitySource) UnmarshalJSON(b []byte) error {
	return jobEnum(b, x, ActivityRunnerExecution, ActivityValidationPlan, ActivityCargoOutput)
}
func ReadJobUpdateRequest(b []byte) (RunnerJobUpdateRequest, error) {
	return read[RunnerJobUpdateRequest](b)
}
func ReadJobInventory(b []byte) (ShellJobInventory, error) { return read[ShellJobInventory](b) }
func ReadJobContext(b []byte) (ShellJobContext, error)     { return read[ShellJobContext](b) }
