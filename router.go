package play

import (
	"io/ioutil"
	"strings"
	"path"
	"regexp"
	"net/http"
)

type Route struct {
	method string  // e.g. GET
	path string    // e.g. /app/{id}
	action string  // e.g. Application.ShowApp

	pathPattern *regexp.Regexp  // for matching the url path
	staticDir string  // e.g. "public" from action "staticDir:public"
	args []*arg // e.g. {id} from path /app/{id}
	actionArgs []string
	actionPattern *regexp.Regexp
}

type RouteMatch struct {
	ControllerName string     // e.g. Application
	FunctionName string       // e.g. ShowApp
	Params []string
	// TODO: Store the param name as well as its order
	// Params map[string]string  // e.g. {id: 123}
	StaticFilename string
}

type arg struct {
	name string
	index int
	constraint *regexp.Regexp
}

// TODO: Use exp/regexp and named groups e.g. (?P<name>a)
var nakedPathParamRegex *regexp.Regexp =
	regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z_0-9]*)\}`)
var argsPattern *regexp.Regexp =
 	regexp.MustCompile(`\{<(?P<pattern>[^>]+)>(?P<var>[a-zA-Z_0-9]+)\}`)

// Prepares the route to be used in matching.
func NewRoute(method, path, action string) (r *Route) {
	r = &Route{
		method: strings.ToUpper(method),
		path: path,
		action: action,
	}

	// Handle static routes
	if strings.HasPrefix(r.action, "staticDir:") {
		if r.method != "*" && r.method != "GET" {
			LOG.Print("W: Static route only supports GET")
			return
		}

		if !strings.HasSuffix(r.path, "/") {
			LOG.Printf("W: The path for staticDir must end with / (%s)", r.path)
			r.path = r.path + "/"
		}

		r.pathPattern = regexp.MustCompile("^" + r.path + "(.*)$")
		r.staticDir = r.action[len("staticDir:"):]
		// TODO: staticFile:
		return
	}

	// URL pattern
	// TODO: Support non-absolute paths
	if !strings.HasPrefix(r.path, "/") {
		LOG.Print("E: Absolute URL required.")
		return
	}

	// Handle embedded arguments

	// Convert path arguments with unspecified regexes to standard form.
	// e.g. "/customer/{id}" => "/customer/{<[^/]+>id}
	normPath := nakedPathParamRegex.ReplaceAllStringFunc(r.path, func(m string) string {
		var argMatches []string = nakedPathParamRegex.FindStringSubmatch(m)
		return "{<[^/]+>" + argMatches[1] + "}"
	})

	// Go through the arguments
	r.args = make([]*arg, 0, 3)
	for i, m := range(argsPattern.FindAllStringSubmatch(normPath, -1)) {
		r.args = append(r.args, &arg{
			name: string(m[2]),
			index: i,
			constraint: regexp.MustCompile(string(m[1])),
		})
	}

	// Now assemble the entire path regex, including the embedded parameters.
	// e.g. /app/{<[^/]+>id} => /app/(?P<id>[^/]+)
	pathPatternStr := argsPattern.ReplaceAllStringFunc(normPath, func(m string) string {
		var argMatches []string = argsPattern.FindStringSubmatch(m)
		return "(?P<" + argMatches[2] + ">" + argMatches[1] + ")"
	})
	r.pathPattern = regexp.MustCompile(pathPatternStr + "$")

	// Handle action
	var actionPatternStr string = strings.Replace(r.action, ".", `\.`, -1)
	for _, arg := range(r.args) {
		var argName string = "{" + arg.name + "}"
		if argIndex := strings.Index(actionPatternStr, argName); argIndex != -1 {
			actionPatternStr = strings.Replace(actionPatternStr, argName,
				"(" + argName + arg.constraint.String() + ")", -1)
			r.actionArgs = append(r.actionArgs, arg.name)
		}
	}
	r.actionPattern = regexp.MustCompile(actionPatternStr)
	LOG.Printf("Path pattern: %s", r.pathPattern)
	return
}

// Return nil if no match.
func (r *Route) Match(method string, reqPath string) *RouteMatch {
	// Check the Method
	if r.method != "*" && method != r.method && !(method == "HEAD" && r.method == "GET") {
		return nil
	}

	// Check the Path
	var matches []string = r.pathPattern.FindStringSubmatch(reqPath)
	if matches == nil {
		return nil
	}
	LOG.Printf("Path Match: %v", matches)

	// If it's a static file request..
	if r.staticDir != "" {
		return &RouteMatch{
			StaticFilename: path.Join(BasePath, r.staticDir, matches[1]),
		}
	}

	// Split the action into controller and function
	actionSplit := strings.Split(r.action, ".")
	if len(actionSplit) != 2 {
		LOG.Printf("E: Failed to split action: %s", r.action)
		return nil
	}
	return &RouteMatch{
		ControllerName: actionSplit[0],
		FunctionName: actionSplit[1],
		Params: matches[1:],
	}
}

type Router struct {
	routes []*Route
}

func (router *Router) Route(req *http.Request) *RouteMatch {
	for _, route := range(router.routes) {
		if m := route.Match(req.Method, req.URL.Path); m != nil {
			return m
		}
	}
	return nil
}

// Groups:
// 1: method
// 4: path
// 5: action
var routePattern *regexp.Regexp = regexp.MustCompile(
	"(?i)^(GET|POST|PUT|DELETE|OPTIONS|HEAD|WS|\\*)" +
	"[(]?([^)]*)(\\))? +" +
	"(.*/[^ ]*) +([^ (]+)(.+)?( *)$")

// Load the routes file.
func LoadRoutes() *Router {
	// Get the routes file content.
	contentBytes, err := ioutil.ReadFile(path.Join(BasePath, "conf", "routes"))
	if err != nil {
		LOG.Fatalln("Failed to load routes file:", err)
	}
	content := string(contentBytes)
	return NewRouter(content)
}

func parseRouteLine(line string) (method, path, action string, found bool) {
	var matches []string = routePattern.FindStringSubmatch(line)
	if matches == nil {
		return
	}
	method, path, action = matches[1], matches[4], matches[5]
	found = true
	return
}

func NewRouter(routesConf string) *Router {
	router := new(Router)
	routes := make([]*Route, 0, 10)

	// For each line..
	for _, line := range(strings.Split(routesConf, "\n")) {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		method, path, action, found := parseRouteLine(line)
		if ! found {
			continue
		}

		route := NewRoute(method, path, action)
		routes = append(routes, route)
	}

	// Convert the List into an array.
	router.routes = routes

	return router
}

type ActionDefinition struct {
	Host, Method, Url, Action string
	Star bool
	Args map[string]interface{}
}

func (a *ActionDefinition) String() string {
	return a.Url
}

func (router *Router) Reverse(action string, args []interface{}) *ActionDefinition {
	for _, route := range router.routes {
		if route.actionPattern == nil {
			continue
		}

		var matches []string = route.actionPattern.FindStringSubmatch(action)
		if len(matches) == 0 {
			continue
		}

		LOG.Println("Reverse routing.  Pattern:", route.actionPattern, "Matching:", action, "Matches:", matches)

		// TODO: Support reversing actions with arguments.
		// This is pending on achieving named parameter passing to actions (rather than positional).
		return &ActionDefinition{Url: route.path}
	}
	LOG.Println("Failed to find reverse route:", action, args)
	return nil
}
