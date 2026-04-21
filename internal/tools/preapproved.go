package tools

import (
	"net/url"
	"strings"
)

// preapprovedHosts mirrors Claude Code's PREAPPROVED_HOSTS
// (/Users/stello/claude-code-src/src/tools/WebFetchTool/preapproved.ts:14-131)
// verbatim, plus two Elnath-specific dogfood additions (finance.naver.com,
// namu.wiki). Entries containing a "/" are path-prefix rules — only the
// listed pathname (or a deeper path segment beneath it) on that host is
// preapproved. Entries without a "/" match the hostname as-is.
//
// SECURITY WARNING (quoted from Claude Code's preapproved.ts header):
// these entries are ONLY for WebFetch GET requests. Do not reuse this list
// for generic sandbox network allow-listing: several hosts (huggingface.co,
// www.kaggle.com, nuget.org) accept file uploads and would be dangerous as
// an unrestricted network rule.
var preapprovedHosts = []string{
	// Anthropic
	"platform.claude.com",
	"code.claude.com",
	"modelcontextprotocol.io",
	"github.com/anthropics",
	"agentskills.io",

	// Top Programming Languages
	"docs.python.org",
	"en.cppreference.com",
	"docs.oracle.com",
	"learn.microsoft.com",
	"developer.mozilla.org",
	"go.dev",
	"pkg.go.dev",
	"www.php.net",
	"docs.swift.org",
	"kotlinlang.org",
	"ruby-doc.org",
	"doc.rust-lang.org",
	"www.typescriptlang.org",

	// Web & JavaScript Frameworks/Libraries
	"react.dev",
	"angular.io",
	"vuejs.org",
	"nextjs.org",
	"expressjs.com",
	"nodejs.org",
	"bun.sh",
	"jquery.com",
	"getbootstrap.com",
	"tailwindcss.com",
	"d3js.org",
	"threejs.org",
	"redux.js.org",
	"webpack.js.org",
	"jestjs.io",
	"reactrouter.com",

	// Python Frameworks & Libraries
	"docs.djangoproject.com",
	"flask.palletsprojects.com",
	"fastapi.tiangolo.com",
	"pandas.pydata.org",
	"numpy.org",
	"www.tensorflow.org",
	"pytorch.org",
	"scikit-learn.org",
	"matplotlib.org",
	"requests.readthedocs.io",
	"jupyter.org",

	// PHP Frameworks
	"laravel.com",
	"symfony.com",
	"wordpress.org",

	// Java Frameworks & Libraries
	"docs.spring.io",
	"hibernate.org",
	"tomcat.apache.org",
	"gradle.org",
	"maven.apache.org",

	// .NET & C# Frameworks
	"asp.net",
	"dotnet.microsoft.com",
	"nuget.org",
	"blazor.net",

	// Mobile Development
	"reactnative.dev",
	"docs.flutter.dev",
	"developer.apple.com",
	"developer.android.com",

	// Data Science & Machine Learning
	"keras.io",
	"spark.apache.org",
	"huggingface.co",
	"www.kaggle.com",

	// Databases
	"www.mongodb.com",
	"redis.io",
	"www.postgresql.org",
	"dev.mysql.com",
	"www.sqlite.org",
	"graphql.org",
	"prisma.io",

	// Cloud & DevOps
	"docs.aws.amazon.com",
	"cloud.google.com",
	// learn.microsoft.com is duplicated in Claude Code upstream (C#/.NET
	// + Azure categories). Go map semantics dedup automatically.
	"learn.microsoft.com",
	"kubernetes.io",
	"www.docker.com",
	"www.terraform.io",
	"www.ansible.com",
	"vercel.com/docs",
	"docs.netlify.com",
	"devcenter.heroku.com",

	// Testing & Monitoring
	"cypress.io",
	"selenium.dev",

	// Game Development
	"docs.unity.com",
	"docs.unrealengine.com",

	// Other Essential Tools
	"git-scm.com",
	"nginx.org",
	"httpd.apache.org",

	// Elnath-specific dogfood additions (Korean-language sites)
	"finance.naver.com",
	"namu.wiki",
}

var (
	preapprovedHostnames    = map[string]struct{}{}
	preapprovedPathPrefixes = map[string][]string{}
)

func init() {
	for _, entry := range preapprovedHosts {
		idx := strings.Index(entry, "/")
		if idx == -1 {
			preapprovedHostnames[entry] = struct{}{}
			continue
		}
		host := entry[:idx]
		path := entry[idx:]
		preapprovedPathPrefixes[host] = append(preapprovedPathPrefixes[host], path)
	}
}

// isPreapprovedHost mirrors Claude Code's isPreapprovedHost
// (/Users/stello/claude-code-src/src/tools/WebFetchTool/preapproved.ts:154-166).
// A hostname-only entry matches any path on that host; a path-prefix entry
// matches only its exact pathname or a deeper segment — so the prefix
// "/anthropics" allows "/anthropics/claude-code" but blocks
// "/anthropics-evil/…".
func isPreapprovedHost(hostname, pathname string) bool {
	if _, ok := preapprovedHostnames[hostname]; ok {
		return true
	}
	prefixes, ok := preapprovedPathPrefixes[hostname]
	if !ok {
		return false
	}
	for _, p := range prefixes {
		if pathname == p || strings.HasPrefix(pathname, p+"/") {
			return true
		}
	}
	return false
}

// isPreapprovedURL parses the raw URL once and delegates to
// isPreapprovedHost. Malformed URLs fail closed.
func isPreapprovedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return isPreapprovedHost(u.Hostname(), u.Path)
}
