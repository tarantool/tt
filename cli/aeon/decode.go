package aeon

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v2/datetime"
	"github.com/tarantool/go-tarantool/v2/decimal"
	"github.com/tarantool/tt/cli/aeon/pb"
)

var (
	errProtobufArrayValueIsNil    = errors.New("protobuf array value is nil")
	errProtobufDatetimeValueIsNil = errors.New("protobuf datetime value is nil")
	errProtobufIntervalValueIsNil = errors.New("protobuf interval value is nil")
	errProtobufMapValueIsNil      = errors.New("protobuf map value is nil")
	errProtobufValueIsNil         = errors.New("protobuf value is nil")
	errUnsupportedTypeForValue    = errors.New("unsupported type for value")
)

// decodeValue convert a value obtained from protobuf into a value that can be used as an
// argument to Tarantool functions.
//
// Copy from https://github.com/tarantool/aeon/blob/master/aeon/grpc/server/pb/decode.go
func decodeValue(val *pb.Value) (any, error) {
	if val == nil {
		return nil, errProtobufValueIsNil
	}

	switch val.GetKind().(type) {
	case *pb.Value_UnsignedValue:
		return val.GetUnsignedValue(), nil
	case *pb.Value_StringValue:
		return val.GetStringValue(), nil
	case *pb.Value_NumberValue:
		return val.GetNumberValue(), nil
	case *pb.Value_IntegerValue:
		return val.GetIntegerValue(), nil
	case *pb.Value_BooleanValue:
		return val.GetBooleanValue(), nil
	case *pb.Value_VarbinaryValue:
		return val.GetVarbinaryValue(), nil
	case *pb.Value_DecimalValue:
		decStr := val.GetDecimalValue()
		res, err := decimal.MakeDecimalFromString(decStr)
		if err != nil {
			return nil, err
		}
		return res, nil
	case *pb.Value_UuidValue:
		uuidStr := val.GetUuidValue()
		res, err := uuid.Parse(uuidStr)
		if err != nil {
			return nil, err
		}
		return res, nil
	case *pb.Value_DatetimeValue:
		dateTime := val.GetDatetimeValue()
		if dateTime == nil {
			return nil, errProtobufDatetimeValueIsNil
		}
		sec := dateTime.GetSeconds()
		nsec := dateTime.GetNsec()
		t := time.Unix(sec, nsec)
		if len(dateTime.GetLocation()) > 0 {
			locStr := dateTime.GetLocation()
			loc, err := time.LoadLocation(locStr)
			if err != nil {
				return nil, err
			}
			t = t.In(loc)
		}
		res, err := datetime.MakeDatetime(t)
		if err != nil {
			return nil, err
		}
		return res, nil
	case *pb.Value_IntervalValue:
		interval := val.GetIntervalValue()
		if interval == nil {
			return nil, errProtobufIntervalValueIsNil
		}
		res := datetime.Interval{
			Year:   interval.GetYear(),
			Month:  interval.GetMonth(),
			Week:   interval.GetWeek(),
			Day:    interval.GetDay(),
			Hour:   interval.GetHour(),
			Min:    interval.GetMin(),
			Sec:    interval.GetSec(),
			Nsec:   interval.GetNsec(),
			Adjust: datetime.Adjust(interval.GetAdjust()),
		}
		return res, nil
	case *pb.Value_ArrayValue:
		array := val.GetArrayValue()
		if array == nil {
			return nil, errProtobufArrayValueIsNil
		}
		fields := array.GetFields()
		res := make([]any, len(fields))
		for k, v := range fields {
			field, err := decodeValue(v)
			if err != nil {
				return nil, err
			}
			res[k] = field
		}
		return res, nil
	case *pb.Value_MapValue:
		mapValue := val.GetMapValue()
		if mapValue == nil {
			return nil, errProtobufMapValueIsNil
		}
		fields := mapValue.GetFields()
		res := make(map[any]any, len(fields))
		for k, v := range fields {
			item, err := decodeValue(v)
			if err != nil {
				return nil, err
			}
			res[k] = item
		}
		return res, nil
	case *pb.Value_NullValue:
		return nil, nil
	default:
		return nil, errUnsupportedTypeForValue
	}
}
