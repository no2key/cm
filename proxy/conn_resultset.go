package proxy

import (
	"github.com/juju/errors"
	"github.com/wandoulabs/cm/hack"
	"github.com/wandoulabs/cm/mysql"
	"github.com/wandoulabs/cm/vt/schema"
)

func formatField(field *mysql.Field, value interface{}) error {
	switch value.(type) {
	case int8, int16, int32, int64, int:
		field.Charset = 63
		field.Type = mysql.MYSQL_TYPE_LONGLONG
		field.Flag = mysql.BINARY_FLAG | mysql.NOT_NULL_FLAG
	case uint8, uint16, uint32, uint64, uint:
		field.Charset = 63
		field.Type = mysql.MYSQL_TYPE_LONGLONG
		field.Flag = mysql.BINARY_FLAG | mysql.NOT_NULL_FLAG | mysql.UNSIGNED_FLAG
	case float32, float64:
		field.Charset = 63
	case string, []byte:
		field.Charset = 33
		field.Type = mysql.MYSQL_TYPE_VARCHAR
	case nil:
		return nil
	default:
		return errors.Errorf("unsupport type %T for resultset", value)
	}
	return nil
}

func (c *Conn) buildResultset(nameTypes []schema.TableColumn, values []mysql.RowValue) (*mysql.Resultset, error) {
	r := &mysql.Resultset{Fields: make([]*mysql.Field, len(nameTypes))}

	var b []byte
	var err error

	for i, vs := range values {
		if len(vs) != len(r.Fields) {
			return nil, errors.Errorf("row %d has %d column not equal %d", i, len(vs), len(r.Fields))
		}

		var row []byte
		for j, value := range vs {
			field := &mysql.Field{}
			if i == 0 {
				r.Fields[j] = field
				//log.Warningf("%+v", nameTypes[i])
				field.Name = hack.Slice(nameTypes[j].Name)
				if err = formatField(field, value); err != nil {
					return nil, errors.Trace(err)
				}
				field.Type = nameTypes[j].SqlType
				field.Charset = uint16(mysql.CollationNames[nameTypes[j].Collation])
				field.IsUnsigned = nameTypes[j].IsUnsigned
			}

			if value == nil {
				row = append(row, "\xfb"...)
			} else {
				b = mysql.Raw(byte(field.Type), value, field.IsUnsigned)
				row = append(row, mysql.PutLengthEncodedString(b, c.alloc)...)
			}
		}

		r.RowDatas = append(r.RowDatas, row)
	}

	return r, nil
}

func (c *Conn) writeResultset(status uint16, r *mysql.Resultset) error {
	c.affectedRows = int64(-1)
	columnLen := mysql.PutLengthEncodedInt(uint64(len(r.Fields)))
	data := c.alloc.AllocBytesWithLen(4, 1024)
	data = append(data, columnLen...)
	if err := c.writePacket(data); err != nil {
		return errors.Trace(err)
	}

	for _, v := range r.Fields {
		data = data[0:4]
		data = append(data, v.Dump(c.alloc)...)
		if err := c.writePacket(data); err != nil {
			return errors.Trace(err)
		}
	}

	if err := c.writeEOF(status); err != nil {
		return errors.Trace(err)
	}

	for _, v := range r.RowDatas {
		data = data[0:4]
		data = append(data, v...)
		if err := c.writePacket(data); err != nil {
			return errors.Trace(err)
		}
	}

	err := c.writeEOF(status)
	if err != nil {
		return errors.Trace(err)
	}

	return errors.Trace(c.flush())
}
