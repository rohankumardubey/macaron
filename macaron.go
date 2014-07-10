// Copyright 2014 Unknown
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

// Package macaron is a high productive and modular design web framework in Go.
package macaron

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"

	"github.com/julienschmidt/httprouter"

	"github.com/Unknwon/macaron/inject"
)

func Version() string {
	return "0.0.1.0709"
}

// Handler can be any callable function.
// Macaron attempts to inject services into the handler's argument list,
// and panics if an argument could not be fullfilled via dependency injection.
type Handler interface{}

// validateHandler makes sure a handler is a callable function,
// and panics if it is not.
func validateHandler(handler Handler) {
	if reflect.TypeOf(handler).Kind() != reflect.Func {
		panic("mocaron handler must be a callable function")
	}
}

type Context struct {
	inject.Injector
	handlers []Handler
	action   Handler
	rw       ResponseWriter
	index    int

	*Render
	params httprouter.Params
}

// Params return value of given param name.
func (c *Context) Params(name string) string {
	return c.params.ByName(name)
}

func (c *Context) handler() Handler {
	if c.index < len(c.handlers) {
		return c.handlers[c.index]
	}
	if c.index == len(c.handlers) {
		return c.action
	}
	panic("invalid index for context handler")
}

func (c *Context) Next() {
	c.index += 1
	c.run()
}

func (c *Context) Written() bool {
	return c.rw.Written()
}

func (c *Context) run() {
	for c.index <= len(c.handlers) {
		vals, err := c.Invoke(c.handler())
		if err != nil {
			panic(err)
		}
		c.index += 1

		// if the handler returned something, write it to the http response
		if len(vals) > 0 {
			ev := c.GetVal(reflect.TypeOf(ReturnHandler(nil)))
			handleReturn := ev.Interface().(ReturnHandler)
			handleReturn(c, vals)
		}

		if c.Written() {
			return
		}
	}
}

// Macaron represents the top level web application.
// inject.Injector methods can be invoked to map services on a global level.
type Macaron struct {
	inject.Injector
	handlers []Handler
	action   Handler
	*Router
	logger *log.Logger
}

// New creates a bare bones Macaron instance.
// Use this method if you want to have full control over the middleware that is used.
func New() *Macaron {
	m := &Macaron{
		Injector: inject.New(),
		action:   func() {},
		Router: &Router{
			router: httprouter.New(),
		},
		logger: log.New(os.Stdout, "[Macaron] ", 0),
	}
	m.Router.m = m
	m.Map(m.logger)
	m.Map(defaultReturnHandler())
	return m
}

// Classic creates a classic Macaron with some basic default middleware:
// mocaron.Logger, mocaron.Recovery and mocaron.Static.
func Classic() *Macaron {
	m := New()
	m.Use(Logger())
	m.Use(Recovery())
	m.Use(Static("public"))
	return m
}

// Handlers sets the entire middleware stack with the given Handlers.
// This will clear any current middleware handlers,
// and panics if any of the handlers is not a callable function
func (m *Macaron) Handlers(handlers ...Handler) {
	m.handlers = make([]Handler, 0)
	for _, handler := range handlers {
		m.Use(handler)
	}
}

// Action sets the handler that will be called after all the middleware has been invoked.
// This is set to macaron.Router in a macaron.Classic().
func (m *Macaron) Action(handler Handler) {
	validateHandler(handler)
	m.action = handler
}

// Use adds a middleware Handler to the stack,
// and panics if the handler is not a callable func.
// Middleware Handlers are invoked in the order that they are added.
func (m *Macaron) Use(handler Handler) {
	validateHandler(handler)
	m.handlers = append(m.handlers, handler)
}

func (m *Macaron) createContext(res http.ResponseWriter, req *http.Request) *Context {
	c := &Context{
		Injector: inject.New(),
		handlers: m.handlers,
		action:   m.action,
		rw:       NewResponseWriter(res),
		index:    0,
	}
	c.SetParent(m)
	c.Map(c)
	c.MapTo(c.rw, (*http.ResponseWriter)(nil))
	c.Map(req)
	return c
}

// ServeHTTP is the HTTP Entry point for a Macaron instance.
// Useful if you want to control your own HTTP server.
// Be aware that none of middleware will run without registering any router.
func (m *Macaron) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	m.router.ServeHTTP(res, req)
}

// Run the http server. Listening on os.GetEnv("PORT") or 4000 by default.
func (m *Macaron) Run() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}

	host := os.Getenv("HOST")

	logger := m.Injector.GetVal(reflect.TypeOf(m.logger)).Interface().(*log.Logger)

	logger.Printf("listening on %s:%s (%s)\n", host, port, Env)
	logger.Fatalln(http.ListenAndServe(host+":"+port, m))
}

// __________               __
// \______   \ ____  __ ___/  |_  ___________
//  |       _//  _ \|  |  \   __\/ __ \_  __ \
//  |    |   (  <_> )  |  /|  | \  ___/|  | \/
//  |____|_  /\____/|____/ |__|  \___  >__|
//         \/                        \/

func (r *Router) addRoute(method string, pattern string, handlers []Handler) {
	if len(r.groups) > 0 {
		groupPattern := ""
		h := make([]Handler, 0)
		for _, g := range r.groups {
			groupPattern += g.pattern
			h = append(h, g.handlers...)
		}

		pattern = groupPattern + pattern
		h = append(h, handlers...)
		handlers = h
	}

	r.router.Handle(method, pattern, func(resp http.ResponseWriter, req *http.Request, params httprouter.Params) {
		c := r.m.createContext(resp, req)
		c.params = params
		c.handlers = handlers
		c.run()
	})
}

type Router struct {
	m      *Macaron
	router *httprouter.Router
	prefx  string
	groups []group
}

type group struct {
	pattern  string
	handlers []Handler
}

func (r *Router) Group(pattern string, fn func(*Router), h ...Handler) {
	r.groups = append(r.groups, group{pattern, h})
	fn(r)
	r.groups = r.groups[:len(r.groups)-1]
}

func (r *Router) Get(pattern string, h ...Handler) {
	r.addRoute("GET", pattern, h)
}

func (r *Router) Patch(pattern string, h ...Handler) {
	r.addRoute("PATCH", pattern, h)
}

func (r *Router) Post(pattern string, h ...Handler) {
	r.addRoute("POST", pattern, h)
}

func (r *Router) Put(pattern string, h ...Handler) {
	r.addRoute("PUT", pattern, h)
}

func (r *Router) Delete(pattern string, h ...Handler) {
	r.addRoute("DELETE", pattern, h)
}

func (r *Router) Options(pattern string, h ...Handler) {
	r.addRoute("OPTIONS", pattern, h)
}

func (r *Router) Head(pattern string, h ...Handler) {
	r.addRoute("HEAD", pattern, h)
}

func (r *Router) NotFound(handlers ...Handler) {
	r.router.NotFound = func(resp http.ResponseWriter, req *http.Request) {
		c := r.m.createContext(resp, req)
		c.handlers = append(r.m.handlers, handlers...)
		c.run()
	}
}

// \_   _____/ _______  __
//  |    __)_ /    \  \/ /
//  |        \   |  \   /
// /_______  /___|  /\_/
//         \/     \/

const (
	DEV  string = "development"
	PROD string = "production"
	TEST string = "test"
)

// Env is the environment that Macaron is executing in.
// The MACARON_ENV is read on initialization to set this variable.
var Env = DEV
var Root string

func setENV(e string) {
	if len(e) > 0 {
		Env = e
	}
}

func init() {
	setENV(os.Getenv("MACARON_ENV"))
	path, err := filepath.Abs(os.Args[0])
	if err != nil {
		panic(err)
	}
	Root = filepath.Dir(path)
}