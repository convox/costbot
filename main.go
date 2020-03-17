package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/costexplorer"
	"github.com/aws/aws-sdk-go/service/organizations"
)

type Cost struct {
	Name string
	Cost float64
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

	ce := costexplorer.New(s)

	res, err := ce.GetCostAndUsage(&costexplorer.GetCostAndUsageInput{
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
			Start: aws.String(time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")),
			End:   aws.String(time.Now().UTC().Format("2006-01-02")),
		},
	})
	if err != nil {
		return err
	}

	costs := []Cost{}

	for _, g := range res.ResultsByTime[0].Groups {
		cost, err := strconv.ParseFloat(*g.Metrics["AmortizedCost"].Amount, 64)
		if err != nil {
			return err
		}

		costs = append(costs, Cost{
			Name: accounts[*g.Keys[0]],
			Cost: cost,
		})
	}

	sort.Slice(costs, func(i, j int) bool {
		return costs[i].Cost > costs[j].Cost
	})

	lines := []string{}

	for _, c := range costs {
		lines = append(lines, fmt.Sprintf("%20s: %0.2f", c.Name, c.Cost))
	}

	p := Payload{
		Blocks: []PayloadBlock{
			PayloadBlock{
				Type: "section",
				Text: PayloadText{
					Type: "mrkdwn",
					Text: fmt.Sprintf("*AWS Daily Run Rate*\n```%s```", strings.Join(lines, "\n")),
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
