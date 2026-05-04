// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package snipool

// defaultEntries returns the embedded starter pool.
//
// The list is hand-curated from publicly visible top-traffic
// rankings (Tranco, Cisco Umbrella) for each region as of early
// 2026. Weights follow a Zipf-like distribution so the highest
// ranks dominate selection while the long tail still draws.
//
// Domains are intentionally limited to ones the Veil project does
// not control. Including project-controlled domains would create a
// trivial self-fingerprint.
//
// This list is a starting point: it will be replaced by signed,
// dynamically-updated Tranco snapshots once the update channel is
// in place.
func defaultEntries() []Entry {
	regions := map[Region][]string{
		RegionGlobal: {
			"google.com",
			"youtube.com",
			"microsoft.com",
			"apple.com",
			"amazon.com",
			"facebook.com",
			"instagram.com",
			"x.com",
			"tiktok.com",
			"linkedin.com",
			"netflix.com",
			"github.com",
			"cloudflare.com",
			"wikipedia.org",
			"reddit.com",
			"telegram.org",
			"spotify.com",
			"twitch.tv",
			"discord.com",
			"zoom.us",
		},
		RegionRU: {
			"yandex.ru",
			"vk.com",
			"mail.ru",
			"ok.ru",
			"sberbank.ru",
			"dzen.ru",
			"kinopoisk.ru",
			"gosuslugi.ru",
			"ozon.ru",
			"wildberries.ru",
			"avito.ru",
			"rambler.ru",
			"lenta.ru",
			"ria.ru",
			"rbc.ru",
		},
		RegionCN: {
			"baidu.com",
			"qq.com",
			"taobao.com",
			"jd.com",
			"weibo.com",
			"sina.com.cn",
			"sohu.com",
			"bilibili.com",
			"tmall.com",
			"alipay.com",
			"163.com",
			"douyin.com",
			"xiaohongshu.com",
			"meituan.com",
			"pinduoduo.com",
		},
		RegionIR: {
			"divar.ir",
			"digikala.com",
			"varzesh3.com",
			"namnak.com",
			"telewebion.com",
			"shaparak.ir",
			"aparat.com",
			"snapp.ir",
			"tarafdari.com",
		},
		RegionEU: {
			"bbc.co.uk",
			"theguardian.com",
			"ebay.co.uk",
			"spiegel.de",
			"lemonde.fr",
			"ouest-france.fr",
			"corriere.it",
			"elpais.com",
			"orange.fr",
			"deutschebank.de",
		},
		RegionUS: {
			"nytimes.com",
			"cnn.com",
			"foxnews.com",
			"yahoo.com",
			"ebay.com",
			"paypal.com",
			"chase.com",
			"hulu.com",
			"target.com",
			"walmart.com",
		},
	}

	var out []Entry
	for region, domains := range regions {
		for i, d := range domains {
			out = append(out, Entry{
				Domain: d,
				Region: region,
				Weight: zipfWeight(i + 1),
			})
		}
	}
	return out
}
