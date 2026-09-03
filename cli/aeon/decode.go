package aeon

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v2/datetime"
	"github.com/tarantool/go-tarantool/v2/decimal"
	"github.com/tarantool/tt/cli/aeon/pb"
)

// decodeValue convert a value obtained from protobuf into a value that can be used as an
// argument to Tarantool functions.
//
// Copy from https://github.com/tarantool/aeon/blob/master/aeon/grpc/server/pb/decode.go
func decodeValue(val *pb.Value) (any, error) {
	switch casted := val.GetKind().(type) {
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
		sec := casted.DatetimeValue.GetSeconds()
		nsec := casted.DatetimeValue.GetNsec()
		t := time.Unix(sec, nsec)

		if len(casted.DatetimeValue.GetLocation()) > 0 {
			locStr := casted.DatetimeValue.GetLocation()

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
		res := datetime.Interval{
			Year:   casted.IntervalValue.GetYear(),
			Month:  casted.IntervalValue.GetMonth(),
			Week:   casted.IntervalValue.GetWeek(),
			Day:    casted.IntervalValue.GetDay(),
			Hour:   casted.IntervalValue.GetHour(),
			Min:    casted.IntervalValue.GetMin(),
			Sec:    casted.IntervalValue.GetSec(),
			Nsec:   casted.IntervalValue.GetNsec(),
			Adjust: datetime.Adjust(casted.IntervalValue.GetAdjust()),
		}

		return res, nil
	case *pb.Value_ArrayValue:
		array := val.GetArrayValue()
		res := make([]any, len(array.GetFields()))

		for k, v := range array.GetFields() {
			field, err := decodeValue(v)
			if err != nil {
				return nil, err
			}

			res[k] = field
		}

		return res, nil
	case *pb.Value_MapValue:
		res := make(map[any]any, len(casted.MapValue.GetFields()))
		for k, v := range casted.MapValue.GetFields() {
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
		return nil, errors.New("unsupported type for value")
	}
}
