package mail

import (
	"bytes"
	"encoding/csv"
	"fmt"
)

var csvColumns = []string{
	"hme", "label", "note", "forwardToEmail", "origin", "isActive",
	"createTimestamp", "anonymousId", "domain", "originAppName", "appBundleId",
	"recipientMailId",
}

func AliasesCSV(aliases []map[string]any) (string, error) {
	buffer := &bytes.Buffer{}
	writer := csv.NewWriter(buffer)
	writer.UseCRLF = false
	if err := writer.Write(csvColumns); err != nil {
		return "", err
	}
	for _, alias := range aliases {
		row := make([]string, len(csvColumns))
		for index, column := range csvColumns {
			row[index] = fmt.Sprint(alias[column])
			if alias[column] == nil {
				row[index] = ""
			}
		}
		if err := writer.Write(row); err != nil {
			return "", err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}
