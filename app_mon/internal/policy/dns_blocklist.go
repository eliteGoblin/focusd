package policy

// DefaultDNSBlocklist is the canonical set of hostnames appmon installs
// into /etc/hosts as `0.0.0.0 <host>` entries. Blocking at the DNS layer
// means no application — Steam, browsers, anything — can resolve these
// domains, regardless of whether they respect system proxy.
//
// macOS's /etc/hosts does exact-match only, no wildcards, so every
// subdomain you care about must be listed explicitly. Each entry below
// is one hostname, written to its own line by the HostsManager.
//
// Subdomains: for any base domain blocked, we always include `www.<d>`,
// and for high-value targets (Steam, YouTube) we also list the API/CDN/
// store subdomains that the apps actually call into. Missing one of
// these subdomains is a real protection gap, not a cosmetic issue.
var DefaultDNSBlocklist = []string{
	// 163.com — Chinese portal / NetEase
	"163.com",
	"www.163.com",
	"mail.163.com",
	"news.163.com",

	// 9news.com.au — news
	"9news.com.au",
	"www.9news.com.au",

	// abc.net.au — Australian Broadcasting Corporation
	"abc.net.au",
	"www.abc.net.au",

	// bilibili.com — Chinese video / anime platform
	"bilibili.com",
	"www.bilibili.com",
	"m.bilibili.com",
	"live.bilibili.com",
	"space.bilibili.com",
	"t.bilibili.com",

	// chronodivide.com — Command & Conquer browser game
	"chronodivide.com",
	"www.chronodivide.com",

	// dos.zone — browser DOS games
	"dos.zone",
	"www.dos.zone",

	// espn.com.au
	"espn.com.au",
	"www.espn.com.au",

	// heheda.top
	"heheda.top",
	"www.heheda.top",

	// iranintl.com
	"iranintl.com",
	"www.iranintl.com",

	// news.com.au
	"news.com.au",
	"www.news.com.au",

	// play-cs.com — Counter-Strike browser
	"play-cs.com",
	"www.play-cs.com",

	// smh.com.au — Sydney Morning Herald
	"smh.com.au",
	"www.smh.com.au",

	// southcn.com
	"southcn.com",
	"www.southcn.com",

	// steamcommunity.com — Steam social
	"steamcommunity.com",
	"www.steamcommunity.com",
	"store.steamcommunity.com",
	"api.steamcommunity.com",

	// steampowered.com — Steam main + API + CDN. Critical to block all
	// subdomains; Steam client uses several distinct hostnames during
	// startup, login, store browsing, and downloads.
	"steampowered.com",
	"www.steampowered.com",
	"store.steampowered.com",
	"api.steampowered.com",
	"community.steampowered.com",
	"partner.steampowered.com",
	"help.steampowered.com",
	"support.steampowered.com",
	"steamcontent.com",
	"steamstatic.com",
	"cdn.akamai.steamstatic.com",
	"cdn.cloudflare.steamstatic.com",
	"media.steampowered.com",

	// theaustralian.com.au
	"theaustralian.com.au",
	"www.theaustralian.com.au",

	// tmtpost.com
	"tmtpost.com",
	"www.tmtpost.com",

	// webrcade.com — browser retro game console
	"webrcade.com",
	"www.webrcade.com",

	// youtube.com — video. Lots of subdomains the app uses.
	"youtube.com",
	"www.youtube.com",
	"m.youtube.com",
	"music.youtube.com",
	"studio.youtube.com",
	"tv.youtube.com",
	"kids.youtube.com",

	// zhihu.com — Chinese Q&A
	"zhihu.com",
	"www.zhihu.com",

	// Dota 2 — installed via Steam but has its own site too
	"dota2.com",
	"www.dota2.com",
	"dota.com",
	"www.dota.com",
}
