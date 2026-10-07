# Third-party notices

Code in Berth derived from other projects, with their copyright notices.
The themes ported from VS Code themes are listed in
[THIRD_PARTY_THEMES.md](THIRD_PARTY_THEMES.md).

## Beautiful UI

| Used in Berth | Source | Author | Licence |
|---|---|---|---|
| The pixel-grid loader (`app/src/components/pixel-loader.tsx`, `pixel-loader.css`): the 3×3 grid, its chevron delays and timing | Loading State, [beautifului.dev](https://www.beautifului.dev/) ([licence](https://www.beautifului.dev/license)) | Copyright (c) 2026 Shane Levine | MIT |

Berth's version is written for its own tokens and motion rules; only the
grid's design and timing come from Beautiful UI. Berth's code block head
and the bar offered by selected words in a chat take visual ideas from
Beautiful UI's Code Block and Selection Actions, with no code from them.

> MIT License
>
> Copyright (c) 2026 Shane Levine
>
> Permission is hereby granted, free of charge, to any person obtaining a
> copy of this software and associated documentation files (the
> "Software"), to deal in the Software without restriction, including
> without limitation the rights to use, copy, modify, merge, publish,
> distribute, sublicense, and/or sell copies of the Software, and to permit
> persons to whom the Software is furnished to do so, subject to the
> following conditions:
>
> The above copyright notice and this permission notice shall be included
> in all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
> OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
> MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN
> NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM,
> DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
> OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE
> USE OR OTHER DEALINGS IN THE SOFTWARE.

## Berth's tmux for Linux boxes

`berth` and the app carry a static tmux for Linux boxes (`tmux-linux-amd64`,
`tmux-linux-arm64`), which `berth add ssh` uploads to a box that has no tmux.
It is tmux, libevent and ncurses, unmodified, built from their release
tarballs by `scripts/build-tmux.sh` and linked statically against musl libc.

| Component | Version | Source | Licence |
|---|---|---|---|
| tmux | 3.7c | https://github.com/tmux/tmux | ISC |
| libevent | 2.1.12-stable | https://libevent.org | 3-clause BSD |
| ncurses | 6.5 | https://invisible-island.net/ncurses/ | MIT (X11) |
| musl libc (as zig ships it) | | https://musl.libc.org | MIT |

### tmux

> Copyright (c) Various Authors
>
> Permission to use, copy, modify, and distribute this software for any
> purpose with or without fee is hereby granted, provided that the above
> copyright notice and this permission notice appear in all copies.
>
> THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
> WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
> MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
> ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
> WHATSOEVER RESULTING FROM LOSS OF MIND, USE, DATA OR PROFITS, WHETHER
> IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING
> OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

### libevent

> Libevent is available for use under the following license, commonly known
> as the 3-clause (or "modified") BSD license:
>
> ==============================
> Copyright (c) 2000-2007 Niels Provos <provos@citi.umich.edu>
> Copyright (c) 2007-2012 Niels Provos and Nick Mathewson
>
> Redistribution and use in source and binary forms, with or without
> modification, are permitted provided that the following conditions
> are met:
> 1. Redistributions of source code must retain the above copyright
>    notice, this list of conditions and the following disclaimer.
> 2. Redistributions in binary form must reproduce the above copyright
>    notice, this list of conditions and the following disclaimer in the
>    documentation and/or other materials provided with the distribution.
> 3. The name of the author may not be used to endorse or promote products
>    derived from this software without specific prior written permission.
>
> THIS SOFTWARE IS PROVIDED BY THE AUTHOR ``AS IS'' AND ANY EXPRESS OR
> IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES
> OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
> IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY DIRECT, INDIRECT,
> INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT
> NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
> DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
> THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
> (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF
> THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
> ==============================
>
> Portions of Libevent are based on works by others, also made available by
> them under the three-clause BSD license above.  The copyright notices are
> available in the corresponding source files; the license is as above.  Here's
> a list:
>
> log.c:
>    Copyright (c) 2000 Dug Song <dugsong@monkey.org>
>    Copyright (c) 1993 The Regents of the University of California.
>
> strlcpy.c:
>    Copyright (c) 1998 Todd C. Miller <Todd.Miller@courtesan.com>
>
> win32select.c:
>    Copyright (c) 2003 Michael A. Davis <mike@datanerds.net>
>
> evport.c:
>    Copyright (c) 2007 Sun Microsystems
>
> ht-internal.h:
>    Copyright (c) 2002 Christopher Clark
>
> minheap-internal.h:
>    Copyright (c) 2006 Maxim Yegorushkin <maxim.yegorushkin@gmail.com>
>
> ==============================
>
> The arc4module is available under the following, sometimes called the
> "OpenBSD" license:
>
>    Copyright (c) 1996, David Mazieres <dm@uun.org>
>    Copyright (c) 2008, Damien Miller <djm@openbsd.org>
>
>    Permission to use, copy, modify, and distribute this software for any
>    purpose with or without fee is hereby granted, provided that the above
>    copyright notice and this permission notice appear in all copies.
>
>    THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
>    WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
>    MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
>    ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
>    WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
>    ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
>    OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
>
> ==============================
>
> The Windows timer code is based on code from libutp, which is
> distributed under this license, sometimes called the "MIT" license.
>
>
> Copyright (c) 2010 BitTorrent, Inc.
>
> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to deal
> in the Software without restriction, including without limitation the rights
> to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
> copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in
> all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
> FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
> AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
> LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
> OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
> THE SOFTWARE.

### ncurses

> Copyright 2018-2023,2024 Thomas E. Dickey
> Copyright 1998-2017,2018 Free Software Foundation, Inc.
>
> Permission is hereby granted, free of charge, to any person obtaining a
> copy of this software and associated documentation files (the
> "Software"), to deal in the Software without restriction, including
> without limitation the rights to use, copy, modify, merge, publish,
> distribute, distribute with modifications, sublicense, and/or sell
> copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included
> in all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
> OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
> MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
> IN NO EVENT SHALL THE ABOVE COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM,
> DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
> OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR
> THE USE OR OTHER DEALINGS IN THE SOFTWARE.
>
> Except as contained in this notice, the name(s) of the above copyright
> holders shall not be used in advertising or otherwise to promote the
> sale, use or other dealings in this Software without prior written
> authorization.
>
> -- $Id: COPYING,v 1.13 2024/01/05 21:13:17 tom Exp $

### musl libc

> Copyright © 2005-2020 Rich Felker, et al.
>
> Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions: The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software. THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
