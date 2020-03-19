package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/costexplorer"
	"github.com/aws/aws-sdk-go/service/organizations"
	"github.com/jinzhu/now"
	"github.com/rodaine/table"
)

type Cost struct {
	Account string
	Name    string
	Daily   float64
	Monthly float64
}

type Payload struct {
	Blocks []PayloadBlock `json:"blocks"`
	Text   string         `json:"text,omitempty"`
}

type PayloadBlock struct {
	Type string      `json:"type"`
	Text PayloadText `json:"text"`
}

type PayloadText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	s, err := session.NewSession()
	if err != nil {
		return err
	}

	o := organizations.New(s)

	ares, err := o.ListAccounts(&organizations.ListAccountsInput{
		MaxResults: aws.Int64(20),
	})
	if err != nil {
		return err
	}

	accounts := map[string]string{}

	for _, a := range ares.Accounts {
		accounts[*a.Id] = *a.Name
	}

	cd, err := costs("DAILY")
	if err != nil {
		return err
	}

	cm, err := costs("MONTHLY")
	if err != nil {
		return err
	}

	cs := []Cost{}

	for k, v := range accounts {
		cs = append(cs, Cost{
			Account: k,
			Name:    v,
			Daily:   cd[k],
			Monthly: cm[k],
		})
	}

	sort.Slice(cs, func(i, j int) bool {
		return cs[i].Monthly > cs[j].Monthly
	})

	var buf bytes.Buffer

	t := table.New("Account", "Day", "Month").WithWriter(&buf)

	for _, c := range cs {
		t.AddRow(c.Name, fmt.Sprintf("%0.2f", c.Daily), fmt.Sprintf("%0.02f", c.Monthly))
	}

	t.Print()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")

	p := Payload{
		Blocks: []PayloadBlock{
			PayloadBlock{
				Type: "section",
				Text: PayloadText{
					Type: "mrkdwn",
					Text: fmt.Sprintf("*AWS Run Rate*\n```%s```", strings.Join(lines, "\n")),
				},
			},
		},
	}

	data, err := json.Marshal(p)
	if err != nil {
		return err
	}

	if _, err := http.Post(os.Getenv("SLACK_WEBHOOK_URL"), "application/json", bytes.NewReader(data)); err != nil {
		return err
	}

	return nil
}

func costs(granularity string) (map[string]float64, error) {
	s, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	start := time.Now().UTC()
	end := time.Now().UTC()

	switch granularity {
	case "DAILY":
		start = time.Now().UTC().Add(-1 * 24 * time.Hour)
	case "MONTHLY":
		start = now.With(start).BeginningOfMonth()
	default:
		return nil, fmt.Errorf("unknown granularity: %s", granularity)
	}

	ce := costexplorer.New(s)

	req := &costexplorer.GetCostAndUsageInput{
		Granularity: aws.String("DAILY"),
		GroupBy: []*costexplorer.GroupDefinition{
			&costexplorer.GroupDefinition{
				Key:  aws.String("LINKED_ACCOUNT"),
				Type: aws.String("DIMENSION"),
			},
		},
		Metrics: []*string{
			aws.String("AmortizedCost"),
			aws.String("BlendedCost"),
			aws.String("NetAmortizedCost"),
			aws.String("NetUnblendedCost"),
			aws.String("NormalizedUsageAmount"),
			aws.String("UnblendedCost"),
			aws.String("UsageQuantity"),
		},
		TimePeriod: &costexplorer.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
	}

	ctx := context.Background()

	p := request.Pagination{
		NewRequest: func() (*request.Request, error) {
			r, _ := ce.GetCostAndUsageRequest(req)
			r.SetContext(ctx)
			return r, nil
		},
	}

	costs := map[string]float64{}

	for p.Next() {
		res := p.Page().(*costexplorer.GetCostAndUsageOutput)

		for _, rt := range res.ResultsByTime {
			for _, g := range rt.Groups {
				cost, err := strconv.ParseFloat(*g.Metrics["AmortizedCost"].Amount, 64)
				if err != nil {
					return nil, err
				}

				if cost > 0 {
					costs[*g.Keys[0]] += cost
				}
			}
		}
	}

	return costs, nil
}
