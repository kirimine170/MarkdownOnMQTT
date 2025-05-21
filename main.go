package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func main() {
	// CLI flags
	broker := flag.String("broker", "tcp://localhost:1883", "MQTT broker URI")
	prefix := flag.String("topic", "markdown", "Topic prefix for publishing")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Println("Usage: go run main.go [flags] <markdown-file>")
		os.Exit(1)
	}
	mdPath := flag.Arg(0)

	// Read Markdown file
	data, err := os.ReadFile(mdPath)
	if err != nil {
		log.Fatalf("Failed to read %s: %v", mdPath, err)
	}

	// Setup MQTT client
	opts := mqtt.NewClientOptions().AddBroker(*broker).SetClientID("md-publisher")
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("MQTT connect error: %v", token.Error())
	}
	defer client.Disconnect(250)

	// Parse Markdown AST
	parser := goldmark.DefaultParser()
	root := parser.Parse(text.NewReader(data))

	var stack []string
	ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := n.(type) {
		case *ast.Heading:
			if entering {
				lvl := node.Level
				if len(stack) >= lvl {
					stack = stack[:lvl-1]
				}
				var txt []byte
				for c := node.FirstChild(); c != nil; c = c.NextSibling() {
					if text, ok := c.(*ast.Text); ok {
						txt = append(txt, text.Segment.Value(data)...)
					}
				}
				title := strings.ReplaceAll(string(txt), " ", "_")
				stack = append(stack, title)
			}
		case *ast.Paragraph:
			if entering {
				var txt []byte
				for c := node.FirstChild(); c != nil; c = c.NextSibling() {
					if text, ok := c.(*ast.Text); ok {
						txt = append(txt, text.Segment.Value(data)...)
					}
				}
				content := strings.TrimSpace(string(txt))
				if content != "" && len(stack) > 0 {
					topic := *prefix + "/" + strings.Join(stack, "/")
					token := client.Publish(topic, 0, false, content)
					token.Wait()
					log.Printf("Published to %s: %s", topic, content)
				}
			}
		}
		return ast.WalkContinue, nil
	})
}
