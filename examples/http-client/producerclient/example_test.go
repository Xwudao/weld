package producerclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/Xwudao/weld/examples/http-client/producerclient"
)

func ExampleClient_WidgetGreet() {
	// Stand in for the producer's typed handler. Real consumers use its base URL.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"msg":  "success",
			"data": map[string]string{
				"module":  "widget",
				"message": "Hello, " + r.URL.Query().Get("name") + "!",
			},
		})
	}))
	defer server.Close()

	client, err := producerclient.New(server.URL)
	if err != nil {
		panic(err)
	}
	response, err := client.WidgetGreet(context.Background(), producerclient.WidgetRequest{Name: "Ada"})
	if err != nil {
		panic(err)
	}
	fmt.Println(response.Message)
	// Output: Hello, Ada!
}
