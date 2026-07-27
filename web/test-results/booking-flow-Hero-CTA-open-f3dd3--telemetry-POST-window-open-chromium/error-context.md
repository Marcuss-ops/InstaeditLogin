# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: booking-flow.spec.ts >> Hero CTA opens modal → 3 questions → Submit fires telemetry POST + window.open
- Location: tests/e2e/booking-flow.spec.ts:41:1

# Error details

```
Test timeout of 30000ms exceeded.
```

```
Error: locator.click: Test timeout of 30000ms exceeded.
Call log:
  - waiting for getByRole('button', { name: /schedule your free strategy call/i })
    - locator resolved to <button type="button" class="group inline-flex items-center gap-2 px-7 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all">…</button>
  - attempting click action
    - waiting for element to be visible, enabled and stable
    - element is not stable
  - retrying click action
    - waiting for element to be visible, enabled and stable
    - element is visible, enabled and stable
    - scrolling into view if needed
    - done scrolling
    - element is outside of the viewport
  - retrying click action
    - waiting 20ms
    2 × waiting for element to be visible, enabled and stable
      - element is visible, enabled and stable
      - scrolling into view if needed
      - done scrolling
      - element is outside of the viewport
    - retrying click action
      - waiting 100ms
    2 × waiting for element to be visible, enabled and stable
      - element is visible, enabled and stable
      - scrolling into view if needed
      - done scrolling
      - <button type="button" tabindex="-1" aria-label="Close booking dialog" class="absolute inset-0 bg-[#020208]/80 backdrop-blur-md"></button> from <div data-state="open" aria-hidden="false" class="fixed inset-0 z-[60] flex items-center justify-center px-4 sm:px-6 py-8 sm:py-12 transition-opacity duration-300 opacity-100 pointer-events-auto">…</div> subtree intercepts pointer events
    - retrying click action
      - waiting 500ms
    - waiting for element to be visible, enabled and stable
    - element is not stable
  - retrying click action
    - waiting 500ms
    - waiting for element to be visible, enabled and stable
    - element is visible, enabled and stable
    - scrolling into view if needed
    - done scrolling
    - <button type="button" tabindex="-1" aria-label="Close booking dialog" class="absolute inset-0 bg-[#020208]/80 backdrop-blur-md"></button> from <div data-state="open" aria-hidden="false" class="fixed inset-0 z-[60] flex items-center justify-center px-4 sm:px-6 py-8 sm:py-12 transition-opacity duration-300 opacity-100 pointer-events-auto">…</div> subtree intercepts pointer events
  7 × retrying click action
      - waiting 500ms
      - waiting for element to be visible, enabled and stable
      - element is not stable
  7 × retrying click action
      - waiting 500ms
      - waiting for element to be visible, enabled and stable
      - element is visible, enabled and stable
      - scrolling into view if needed
      - done scrolling
      - <button type="button" tabindex="-1" aria-label="Close booking dialog" class="absolute inset-0 bg-[#020208]/80 backdrop-blur-md"></button> from <div data-state="open" aria-hidden="false" class="fixed inset-0 z-[60] flex items-center justify-center px-4 sm:px-6 py-8 sm:py-12 transition-opacity duration-300 opacity-100 pointer-events-auto">…</div> subtree intercepts pointer events
    - retrying click action
      - waiting 500ms
      - waiting for element to be visible, enabled and stable
      - element is not stable
  2 × retrying click action
      - waiting 500ms
      - waiting for element to be visible, enabled and stable
      - element is not stable
  - retrying click action
    - waiting 500ms

```

# Page snapshot

```yaml
- generic [ref=f1e3]:
  - dialog "Avviso cookie" [ref=f1e4]:
    - generic [ref=f1e9]:
      - paragraph [ref=f1e10]: Utilizziamo i cookie
      - paragraph [ref=f1e11]:
        - text: Impostiamo un solo cookie essenziale per mantenerti autenticato. Non usiamo cookie di tracciamento o analytics. Consulta la
        - link "privacy policy" [ref=f1e12] [cursor=pointer]:
          - /url: /privacy
        - text: .
      - generic [ref=f1e13]:
        - button "Solo essenziali" [ref=f1e14]
        - button "Accetta tutti" [ref=f1e15]
  - navigation [ref=f1e16]:
    - generic [ref=f1e18]:
      - link "InstaEdit" [ref=f1e19] [cursor=pointer]:
        - /url: /
      - generic [ref=f1e24]:
        - generic [ref=f1e25]:
          - link "How it works" [ref=f1e26] [cursor=pointer]:
            - /url: "#features"
          - link "Programs" [ref=f1e27] [cursor=pointer]:
            - /url: "#programs"
          - link "Results" [ref=f1e28] [cursor=pointer]:
            - /url: "#results"
          - link "FAQ" [ref=f1e29] [cursor=pointer]:
            - /url: "#faq"
          - link "Contact" [ref=f1e30] [cursor=pointer]:
            - /url: "#contact"
        - button "Schedule a Call" [ref=f1e31]:
          - text: Schedule a Call
          - generic [ref=f1e34]: Free
  - generic [ref=f1e36]:
    - generic [ref=f1e37]:
      - generic [ref=f1e38]:
        - generic [ref=f1e39]: Turnkey system — zero experience needed
        - generic [ref=f1e44]: Limited — 10 new clients this month
      - heading "Your First $2,000/Mo From Video On Autopilot — No Experience Needed." [level=1] [ref=f1e49]
      - paragraph [ref=f1e50]: Stop wasting months figuring out the algorithm. We give you an already-monetized YouTube channel, AI that creates the videos for you, and 1-on-1 coaching to get you to your first payout — fast.
      - generic [ref=f1e51]:
        - button "Schedule Your Free Strategy Call" [ref=f1e52]
        - link "See Real Results" [ref=f1e57] [cursor=pointer]:
          - /url: "#proof"
      - generic [ref=f1e58]:
        - generic [ref=f1e59]:
          - generic [ref=f1e62]: $2,150/mo
          - generic [ref=f1e63]: avg. student income
        - generic [ref=f1e64]:
          - generic [ref=f1e68]: 14 days
          - generic [ref=f1e69]: avg. first payout
        - generic [ref=f1e70]:
          - generic [ref=f1e74]: 100%
          - generic [ref=f1e75]: AI-automated
    - generic [ref=f1e77]:
      - generic [ref=f1e78]:
        - generic [ref=f1e79]:
          - generic [ref=f1e84]: instaedit.app · Calendar
          - generic [ref=f1e85]: Live
        - generic [ref=f1e88]:
          - generic [ref=f1e89]:
            - generic [ref=f1e90]: "12"
            - generic [ref=f1e91]: Scheduled
          - generic [ref=f1e92]:
            - generic [ref=f1e93]: "4"
            - generic [ref=f1e94]: Platforms
          - generic [ref=f1e95]:
            - generic [ref=f1e96]: 7d
            - generic [ref=f1e97]: Window
          - generic [ref=f1e98]:
            - generic [ref=f1e99]: +
            - generic [ref=f1e100]: New
        - generic [ref=f1e101]:
          - generic [ref=f1e102]: Scheduled
          - generic [ref=f1e103]: All
          - generic [ref=f1e104]: Drafts
          - generic [ref=f1e105]: Published
          - generic [ref=f1e106]: New post
        - list [ref=f1e108]:
          - listitem [ref=f1e109]:
            - generic [ref=f1e111]:
              - generic [ref=f1e112]: "Behind the scenes: shipping our AI pipeline"
              - generic [ref=f1e113]:
                - generic [ref=f1e114]:
                  - generic "Instagram" [ref=f1e115]
                  - generic "LinkedIn" [ref=f1e120]
                  - generic "YouTube" [ref=f1e126]
                - generic [ref=f1e130]: · Vertical · auto-captioned
            - generic [ref=f1e131]: Tomorrow · 09:00
          - listitem [ref=f1e132]:
            - generic [ref=f1e134]:
              - generic [ref=f1e135]: Why async publishing beats 10-person teams
              - generic [ref=f1e136]:
                - generic [ref=f1e137]:
                  - generic "LinkedIn" [ref=f1e138]
                  - generic "Facebook" [ref=f1e144]
                - generic [ref=f1e148]: · Horizontal · approved by Ana
            - generic [ref=f1e149]: Wed · 14:00
          - listitem [ref=f1e150]:
            - generic [ref=f1e152]:
              - generic [ref=f1e153]: Quarterly retrospective
              - generic [ref=f1e154]:
                - generic [ref=f1e155]:
                  - generic "Instagram" [ref=f1e156]
                  - generic "TikTok" [ref=f1e161]
                  - generic "X" [ref=f1e167]
                - generic [ref=f1e171]: · Vertical · captions live
            - generic [ref=f1e172]: Fri · 10:00
          - listitem [ref=f1e173]:
            - generic [ref=f1e175]:
              - generic [ref=f1e176]: "10,000 pieces of content: how we ship"
              - generic [ref=f1e177]:
                - generic [ref=f1e178]:
                  - generic "YouTube" [ref=f1e179]
                  - generic "Instagram" [ref=f1e183]
                - generic [ref=f1e188]: · Horizontal · thumbnail A/B
            - generic [ref=f1e189]: Mon · 08:00
        - generic [ref=f1e190]:
          - generic [ref=f1e191]: 12 of 28 posts scheduled this week
          - generic [ref=f1e192]: Auto-publishing active
      - generic [ref=f1e197]:
        - generic [ref=f1e198]: 200 → 8 posts
        - generic [ref=f1e199]: in one click
  - generic [ref=f1e202]:
    - generic [ref=f1e203]:
      - generic [ref=f1e204]: The problem
      - heading "You're losing money every single day." [level=2] [ref=f1e205]
      - paragraph [ref=f1e206]: You want to earn online but editing steals 15 hours a week. You're scared to show your face or don't know where to start. You've been creating for months — 50 views and zero dollars.
      - generic [ref=f1e207]:
        - generic [ref=f1e208]: Editing eats 15+ hours per week and you see no return
        - generic [ref=f1e212]: You're afraid to show your face or don't know the tech side
        - generic [ref=f1e216]: You've posted for months — 50 views and $0 earned
        - generic [ref=f1e220]: The algorithm rewards daily posting but you can't keep up
    - generic [ref=f1e224]:
      - generic [ref=f1e225]: The InstaEdit shortcut
      - heading "The easy way to real income." [level=2] [ref=f1e226]
      - paragraph [ref=f1e227]: "We hand you the keys: a channel already past YouTube's trust filter, AI that produces professional videos from a single line of text, and a mentor who tells you exactly what to post and when."
      - generic [ref=f1e228]:
        - generic [ref=f1e229]: Ready-made channel — you skip the "grind" phase entirely
        - generic [ref=f1e235]: "ChronoN AI: type one sentence, get a ready-to-publish video"
        - generic [ref=f1e241]: One video becomes 7 posts across 7 platforms automatically
        - generic [ref=f1e247]: Daily content without lifting a finger — 100% hands-free
  - generic [ref=f1e254]:
    - generic [ref=f1e255]:
      - generic [ref=f1e256]: Earnings
      - heading "How much can you earn?" [level=2] [ref=f1e257]
      - paragraph [ref=f1e258]: Realistic income ranges based on our current students. The more channels you automate, the more revenue you generate.
    - generic [ref=f1e259]:
      - generic [ref=f1e260]:
        - generic [ref=f1e264]: 1 Automated Channel
        - generic [ref=f1e265]:
          - generic [ref=f1e266]: $500 – $1,500
          - generic [ref=f1e267]: /mo
        - paragraph [ref=f1e268]: A single AI-managed channel in a profitable niche
      - generic [ref=f1e269]:
        - generic [ref=f1e273]: 3 Channels (Multi-language)
        - generic [ref=f1e274]:
          - generic [ref=f1e275]: $2,000 – $5,000
          - generic [ref=f1e276]: /mo
        - paragraph [ref=f1e277]: Multiple channels across English, Spanish & Portuguese
      - generic [ref=f1e278]:
        - generic [ref=f1e282]: Channel Portfolio (Level 3)
        - generic [ref=f1e283]:
          - generic [ref=f1e284]: $10,000+
          - generic [ref=f1e285]: /mo
        - paragraph [ref=f1e286]: Full network of monetized channels with global reach
  - generic [ref=f1e288]:
    - generic [ref=f1e289]:
      - generic [ref=f1e290]: How it works
      - heading "Zero effort. Maximum income." [level=2] [ref=f1e291]
      - paragraph [ref=f1e292]: No camera. No editing. No experience. Our AI-powered system turns a single idea into daily content across 7 platforms — engineered to generate revenue.
    - generic [ref=f1e293]:
      - generic [ref=f1e297]:
        - heading "AI creates. You earn." [level=3] [ref=f1e302]
        - paragraph [ref=f1e303]: Type one sentence and ChronoN AI generates a professional, monetization-ready video. No camera, no microphone, no editing software. The AI handles everything from script to final render.
        - generic [ref=f1e304]:
          - generic [ref=f1e305]: Autopilot · this week
          - generic [ref=f1e310]:
            - generic [ref=f1e311]: M
            - generic [ref=f1e316]: T
            - generic [ref=f1e320]: W
            - generic [ref=f1e325]: T
            - generic [ref=f1e329]: F
            - generic [ref=f1e334]: S
            - generic [ref=f1e338]: S
      - generic [ref=f1e343]:
        - heading "7 platforms, 1 video." [level=3] [ref=f1e348]
        - paragraph [ref=f1e349]: One AI-generated video is automatically converted and published to YouTube, TikTok, Instagram Reels, Facebook, X and more — multiplying your reach by 7x.
      - generic [ref=f1e350]:
        - heading "Revenue from day one." [level=3] [ref=f1e354]
        - paragraph [ref=f1e355]: Aged channels skip YouTube's trust filter. You hit the Partner Program faster and start earning ad revenue, sponsorships and affiliate income sooner.
      - generic [ref=f1e357]:
        - generic [ref=f1e358]:
          - heading "Scale to multiple languages." [level=3] [ref=f1e363]
          - paragraph [ref=f1e364]: Expand your channel portfolio to Spanish, Portuguese, French, German and more — all powered by AI translation and localization. Reach global audiences without learning a new language.
        - generic [ref=f1e379]:
          - generic [ref=f1e380]: Jan
          - generic [ref=f1e381]: Mar
          - generic [ref=f1e382]: May
          - generic [ref=f1e383]: Jul
          - generic [ref=f1e384]: Sep
          - generic [ref=f1e385]: Nov
  - generic [ref=f1e387]:
    - generic [ref=f1e388]:
      - generic [ref=f1e389]: Results
      - heading "Real people. Real income." [level=2] [ref=f1e390]
      - paragraph [ref=f1e391]: Most creators spend months earning nothing. Our students hit their first payout in under two weeks and build a recurring monthly income on autopilot.
    - generic [ref=f1e392]:
      - generic [ref=f1e393]:
        - generic [ref=f1e396]: $2,150
        - generic [ref=f1e397]: Avg. student income
        - generic [ref=f1e398]: per month, per channel
      - generic [ref=f1e399]:
        - generic [ref=f1e403]: 14 days
        - generic [ref=f1e404]: Avg. first payout
        - generic [ref=f1e405]: from channel start
      - generic [ref=f1e406]:
        - generic [ref=f1e412]: 50+
        - generic [ref=f1e413]: Channels monetized
        - generic [ref=f1e414]: and generating revenue
      - generic [ref=f1e415]:
        - generic [ref=f1e418]: 100%
        - generic [ref=f1e419]: AI-automated
        - generic [ref=f1e420]: zero editing required
    - generic [ref=f1e421]:
      - generic [ref=f1e422]:
        - generic [ref=f1e424]:
          - generic [ref=f1e425]:
            - generic [ref=f1e426]: M
            - generic [ref=f1e427]:
              - generic [ref=f1e428]: Marcus T.
              - generic [ref=f1e429]: Start & Earn student
          - generic [ref=f1e430]: "First payout: Day 20"
        - generic [ref=f1e431]:
          - generic [ref=f1e432]: YouTube Studio · Earnings
          - generic [ref=f1e435]:
            - generic [ref=f1e436]: $2,150.00
            - generic [ref=f1e437]: this month
        - paragraph [ref=f1e451]: “Day 20 I hit my first monetization thanks to the aged channel and my mentor's guidance. I never thought it could be this fast.”
      - generic [ref=f1e452]:
        - generic [ref=f1e454]:
          - generic [ref=f1e455]:
            - generic [ref=f1e456]: S
            - generic [ref=f1e457]:
              - generic [ref=f1e458]: Sarah L.
              - generic [ref=f1e459]: Done-For-You member
          - generic [ref=f1e460]: "Current income: $1,800/mo"
        - generic [ref=f1e461]:
          - generic [ref=f1e462]: YouTube Studio · Earnings
          - generic [ref=f1e465]:
            - generic [ref=f1e466]: $2,150.00
            - generic [ref=f1e467]: this month
        - paragraph [ref=f1e481]: “I went from zero to $1,800/mo in 6 weeks. The AI does all the editing — I just approve the scripts. It's genuinely passive.”
      - generic [ref=f1e482]:
        - generic [ref=f1e484]:
          - generic [ref=f1e485]:
            - generic [ref=f1e486]: D
            - generic [ref=f1e487]:
              - generic [ref=f1e488]: David K.
              - generic [ref=f1e489]: Start & Earn student
          - generic [ref=f1e490]: 1K subs in 12 days
        - generic [ref=f1e491]:
          - generic [ref=f1e492]: YouTube Studio · Earnings
          - generic [ref=f1e495]:
            - generic [ref=f1e496]: $2,150.00
            - generic [ref=f1e497]: this month
        - paragraph [ref=f1e511]: “The aged channel was the game-changer. My videos got indexed in hours instead of weeks. I hit 1,000 subs in 12 days.”
      - generic [ref=f1e512]:
        - generic [ref=f1e514]:
          - generic [ref=f1e515]:
            - generic [ref=f1e516]: A
            - generic [ref=f1e517]:
              - generic [ref=f1e518]: Ana R.
              - generic [ref=f1e519]: Channel Portfolio member
          - generic [ref=f1e520]: 4 channels live
        - generic [ref=f1e521]:
          - generic [ref=f1e522]: YouTube Studio · Earnings
          - generic [ref=f1e525]:
            - generic [ref=f1e526]: $2,150.00
            - generic [ref=f1e527]: this month
        - paragraph [ref=f1e541]: “I manage 4 channels now, all automated. Portfolio-level is where the real money starts. Each one pays for itself in week one.”
    - generic [ref=f1e542]:
      - img "YouTube channel growth result" [ref=f1e545]
      - img "Content strategy result" [ref=f1e548]
      - img "Channel monetization result" [ref=f1e551]
      - img "Video performance result" [ref=f1e554]
      - img "Creator growth result" [ref=f1e557]
      - img "Multi-platform result" [ref=f1e560]
  - generic [ref=f1e562]:
    - generic [ref=f1e563]:
      - generic [ref=f1e564]: Proof
      - heading "See the channels that are earning." [level=2] [ref=f1e565]
      - paragraph [ref=f1e566]: These are real channels built and automated by our system. Watch the content our students' channels are publishing — and the revenue they generate.
    - generic [ref=f1e567]:
      - iframe [ref=f1e570]:
        - generic [ref=f2e1]:
          - generic "YouTube Video Player" [ref=f2e3]
          - generic [ref=f2e5]:
            - generic:
              - generic:
                - button "Play video" [ref=f2e10] [cursor=pointer]
                - button "Hide player controls" [ref=f2e14] [cursor=pointer]
                - generic [ref=f2e21]:
                  - generic [ref=f2e22]:
                    - link "Speed's Curse Failed When He Needed It Most 😂😂" [ref=f2e23] [cursor=pointer]:
                      - /url: https://www.youtube.com/shorts/MVwXsmRLnwM
                    - link "deagzzzshorts" [ref=f2e24] [cursor=pointer]:
                      - /url: /channel/UCjcZfaPzpgifneY4aZgLGrQ
                  - generic [ref=f2e26]:
                    - button [ref=f2e27] [cursor=pointer]
                    - generic [ref=f2e29]:
                      - generic: deagzzzshorts
                      - generic: 6.18M subscribers
      - iframe [ref=f1e573]:
        - generic [ref=f3e1]:
          - generic "YouTube Video Player" [ref=f3e3]
          - generic [ref=f3e5]:
            - generic:
              - generic:
                - button "Play video" [ref=f3e10] [cursor=pointer]
                - button "Hide player controls" [ref=f3e14] [cursor=pointer]
                - generic [ref=f3e21]:
                  - generic [ref=f3e22]:
                    - link "Bro Predicted Absolutely Nothing 🥀🥀" [ref=f3e23] [cursor=pointer]:
                      - /url: https://www.youtube.com/shorts/XCIWzK2BuRo
                    - link "deagzzzshorts" [ref=f3e24] [cursor=pointer]:
                      - /url: /channel/UCjcZfaPzpgifneY4aZgLGrQ
                  - generic [ref=f3e26]:
                    - button [ref=f3e27] [cursor=pointer]
                    - generic [ref=f3e29]:
                      - generic: deagzzzshorts
                      - generic: 6.18M subscribers
      - iframe [ref=f1e576]:
        - generic [ref=f4e1]:
          - generic "YouTube Video Player" [ref=f4e3]
          - generic [ref=f4e5]:
            - generic:
              - generic:
                - button "Play video" [ref=f4e10] [cursor=pointer]
                - button "Hide player controls" [ref=f4e12] [cursor=pointer]
                - generic [ref=f4e19]:
                  - generic [ref=f4e20]:
                    - link "Diddy PANICS After CNN Releases NEW Footage Incriminating Him" [ref=f4e21] [cursor=pointer]:
                      - /url: https://www.youtube.com/watch?v=fLhv7d6N_3c
                    - link "Industry Secrets" [ref=f4e22] [cursor=pointer]:
                      - /url: /channel/UC2hIFNpT5cM4qPUegD6VH1Q
                  - generic [ref=f4e24]:
                    - button [ref=f4e25] [cursor=pointer]
                    - generic [ref=f4e27]:
                      - generic: Industry Secrets
                      - generic: 127K subscribers
      - iframe [ref=f1e579]:
        - generic [ref=f5e1]:
          - generic "YouTube Video Player" [ref=f5e3]
          - generic [ref=f5e5]:
            - generic:
              - generic:
                - button "Play video" [ref=f5e10] [cursor=pointer]
                - button "Hide player controls" [ref=f5e12] [cursor=pointer]
                - generic [ref=f5e19]:
                  - generic [ref=f5e20]:
                    - link "The Avengers Couldnt Do SH!T To Thanos 😭😭😭" [ref=f5e21] [cursor=pointer]:
                      - /url: https://www.youtube.com/watch?v=iA1WT69NFbw
                    - link "Zephfire" [ref=f5e22] [cursor=pointer]:
                      - /url: /channel/UCtBULndzjepcpwDq7hKYV6Q
                  - generic [ref=f5e24]:
                    - button [ref=f5e25] [cursor=pointer]
                    - generic [ref=f5e27]:
                      - generic: Zephfire
                      - generic: 753K subscribers
    - generic [ref=f1e580]:
      - iframe [ref=f1e583]:
        - generic [ref=f6e1]:
          - generic "YouTube Video Player" [ref=f6e3]
          - generic [ref=f6e5]:
            - generic:
              - generic:
                - button "Play video" [ref=f6e10] [cursor=pointer]
                - button "Hide player controls" [ref=f6e12] [cursor=pointer]
                - generic [ref=f6e14]:
                  - generic [ref=f6e19]:
                    - generic [ref=f6e20]:
                      - link "The FITNESS Iceberg Explained" [ref=f6e21] [cursor=pointer]:
                        - /url: https://www.youtube.com/watch?v=R18AVWQ92fs
                      - link "Back Guy" [ref=f6e22] [cursor=pointer]:
                        - /url: /channel/UCwlxDWqcPnozHVQMzTeg4SQ
                    - generic [ref=f6e24]:
                      - button [ref=f6e25] [cursor=pointer]
                      - generic [ref=f6e27]:
                        - generic: Back Guy
                        - generic: 609K subscribers
                  - generic [ref=f6e28]:
                    - button "Share" [ref=f6e31] [cursor=pointer]
                    - link "Watch on YouTube" [ref=f6e42] [cursor=pointer]:
                      - /url: https://www.youtube.com/watch?v=R18AVWQ92fs
                      - generic [ref=f6e43]: Watch on
      - iframe [ref=f1e586]:
        - generic [ref=f7e1]:
          - generic "YouTube Video Player" [ref=f7e3]
          - generic [ref=f7e5]:
            - generic:
              - generic:
                - button "Play video" [ref=f7e10] [cursor=pointer]
                - button "Hide player controls" [ref=f7e12] [cursor=pointer]
                - generic [ref=f7e14]:
                  - generic [ref=f7e19]:
                    - generic [ref=f7e20]:
                      - link "It's Not Your Phone Killing Your Mornings." [ref=f7e21] [cursor=pointer]:
                        - /url: https://www.youtube.com/watch?v=lpKX9SKqSMw
                      - link "Rian Doris" [ref=f7e22] [cursor=pointer]:
                        - /url: /channel/UCftSbpEaMtTWcaFnvjwCvXw
                    - generic [ref=f7e24]:
                      - button [ref=f7e25] [cursor=pointer]
                      - generic [ref=f7e27]:
                        - generic: Rian Doris
                        - generic: 500K subscribers
                  - generic [ref=f7e28]:
                    - button "Share" [ref=f7e31] [cursor=pointer]
                    - link "Watch on YouTube" [ref=f7e42] [cursor=pointer]:
                      - /url: https://www.youtube.com/watch?v=lpKX9SKqSMw
                      - generic [ref=f7e43]: Watch on
  - generic [ref=f1e588]:
    - generic [ref=f1e589]:
      - generic [ref=f1e590]: Choose your path
      - heading "Three ways to build passive income." [level=2] [ref=f1e591]
      - paragraph [ref=f1e592]: Whether you want to learn the system, have us run everything, or scale a full portfolio — the income is real and the path is clear.
    - generic [ref=f1e593]:
      - generic [ref=f1e595]:
        - generic [ref=f1e602]:
          - generic [ref=f1e603]: Level 1
          - generic [ref=f1e604]: Guided & fast income
        - heading "Start & Earn" [level=3] [ref=f1e605]
        - paragraph [ref=f1e606]: From zero to your first monthly paycheck in 30 days.
        - paragraph [ref=f1e607]: The guided path for anyone who wants to start earning right away. You get an already-monetized channel, a personal mentor, and a proven system to reach your first payout in 30 days.
        - list [ref=f1e608]:
          - listitem [ref=f1e609]:
            - generic [ref=f1e613]: Aged YouTube channel included — skip the algorithm's "grind" phase
          - listitem [ref=f1e614]:
            - generic [ref=f1e618]: 1-on-1 personal mentor on the highest-paying niches
          - listitem [ref=f1e619]:
            - generic [ref=f1e623]: Zero editing — AI templates do all the work for you
          - listitem [ref=f1e624]:
            - generic [ref=f1e628]: Step-by-step roadmap to your first $1,000/mo
        - button "Book the Starter Call" [ref=f1e629]
      - generic [ref=f1e636]:
        - generic [ref=f1e641]:
          - generic [ref=f1e642]: Level 2
          - generic [ref=f1e643]: 100% passive
        - heading "Done-For-You" [level=3] [ref=f1e644]
        - paragraph [ref=f1e645]: No camera, no voice, no time spent. We do everything.
        - paragraph [ref=f1e646]: We create, optimize, and publish daily content for you. Your channel generates revenue while you sleep.
        - list [ref=f1e647]:
          - listitem [ref=f1e648]:
            - generic [ref=f1e652]: Full-auto management across 7 platforms from day one
          - listitem [ref=f1e653]:
            - generic [ref=f1e657]: 5 channels + 10 AI videos included immediately
          - listitem [ref=f1e658]:
            - generic [ref=f1e662]: Revenue from YouTube Shorts, TikTok, Reels & sponsorships
          - listitem [ref=f1e663]:
            - generic [ref=f1e667]: Daily optimized content — you do nothing
        - button "Book the Premium Call" [ref=f1e668]
      - generic [ref=f1e674]:
        - generic [ref=f1e680]:
          - generic [ref=f1e681]: Level 3
          - generic [ref=f1e682]: Scale & multiply
        - heading "Channel Portfolio" [level=3] [ref=f1e683]
        - paragraph [ref=f1e684]: From one channel to a global content empire.
        - paragraph [ref=f1e685]: Turn one monetized channel into a full portfolio. Expand into multiple languages and niches with unlimited AI content and dedicated infrastructure.
        - list [ref=f1e686]:
          - listitem [ref=f1e687]:
            - generic [ref=f1e691]: Portfolio-wide automation and analytics dashboard
          - listitem [ref=f1e692]:
            - generic [ref=f1e696]: Multi-language expansion for global reach
          - listitem [ref=f1e697]:
            - generic [ref=f1e701]: Unlimited AI-generated videos across all channels
          - listitem [ref=f1e702]:
            - generic [ref=f1e706]: Dedicated infrastructure and priority support
        - button "Book the Portfolio Call" [ref=f1e707]
  - generic [ref=f1e713]:
    - generic [ref=f1e714]:
      - generic [ref=f1e715]: FAQ
      - heading "Questions? We've got answers." [level=2] [ref=f1e716]
    - generic [ref=f1e717]:
      - button "Do I need any experience or technical skills?" [ref=f1e719]
      - button "How can I start earning money in just 14 days?" [ref=f1e724]
      - button "What is an aged YouTube channel and why does it matter?" [ref=f1e729]
      - button "What's the difference between Mentoring and Done-For-You?" [ref=f1e734]
      - button "How much time do I need to commit each week?" [ref=f1e739]
      - button "How much can I realistically earn per month?" [ref=f1e744]
  - generic [ref=f1e750]:
    - generic [ref=f1e751]: Limited spots — we accept only 10 new students this month to guarantee 1-on-1 support
    - heading "Ready to turn YouTube into your monthly paycheck?" [level=2] [ref=f1e755]
    - paragraph [ref=f1e756]: Book a free strategy call and we'll map out exactly how you'll reach your first $2,000/mo — even if you have zero experience and zero subscribers today.
    - generic [ref=f1e757]:
      - button "Schedule My Free Call" [ref=f1e758]
      - link "Prefer to chat first? Discord @instaedit" [ref=f1e763] [cursor=pointer]:
        - /url: https://discord.com/users/1201477873719050332
        - generic [ref=f1e767]:
          - text: Prefer to chat first?
          - generic [ref=f1e768]: Discord @instaedit
  - contentinfo [ref=f1e769]:
    - generic [ref=f1e770]:
      - generic [ref=f1e771]:
        - link "InstaEdit" [ref=f1e772] [cursor=pointer]:
          - /url: /
        - paragraph [ref=f1e777]: Turn YouTube into passive income. We handle the channel, the content and the monetization — you collect the revenue.
        - generic [ref=f1e778]: Automated income · zero effort
      - generic [ref=f1e781]:
        - generic [ref=f1e782]:
          - generic [ref=f1e783]: Product
          - list [ref=f1e784]:
            - listitem [ref=f1e785]:
              - link "How it works" [ref=f1e786] [cursor=pointer]:
                - /url: "#features"
            - listitem [ref=f1e787]:
              - link "Programs" [ref=f1e788] [cursor=pointer]:
                - /url: "#programs"
            - listitem [ref=f1e789]:
              - link "Results" [ref=f1e790] [cursor=pointer]:
                - /url: "#results"
            - listitem [ref=f1e791]:
              - link "FAQ" [ref=f1e792] [cursor=pointer]:
                - /url: "#faq"
        - generic [ref=f1e793]:
          - generic [ref=f1e794]: Legal
          - list [ref=f1e795]:
            - listitem [ref=f1e796]:
              - link "Privacy" [ref=f1e797] [cursor=pointer]:
                - /url: /privacy
            - listitem [ref=f1e798]:
              - link "Terms" [ref=f1e799] [cursor=pointer]:
                - /url: /terms
            - listitem [ref=f1e800]:
              - link "Data deletion" [ref=f1e801] [cursor=pointer]:
                - /url: /data-deletion.html
    - generic [ref=f1e803]:
      - generic [ref=f1e804]: © 2026 InstaEdit, Inc.
      - generic [ref=f1e805]: Built for creators who want passive income.
  - generic [ref=f1e806]:
    - button "Close booking dialog" [ref=f1e807]
    - dialog [ref=f1e808]:
      - button "Close dialog" [active] [ref=f1e810]
      - generic [ref=f1e814]:
        - banner [ref=f1e815]:
          - generic [ref=f1e816]:
            - generic [ref=f1e817]: Limited — 10 new clients this month
            - generic [ref=f1e822]: Tier 1 · Starter
          - heading "Schedule your free strategy call." [level=2] [ref=f1e827]
          - paragraph [ref=f1e828]: Three quick questions so we map the right plan in 30 minutes — even if you're starting from scratch.
        - generic [ref=f1e829]:
          - group [ref=f1e830]:
            - generic [ref=f1e831]:
              - generic [ref=f1e832]: "01"
              - generic [ref=f1e833]:
                - generic [ref=f1e834]: What is your primary goal right now?
                - paragraph [ref=f1e835]: Pick the path that matches where you are today.
            - radiogroup "Primary goal" [ref=f1e836]:
              - radio "Tier 1 · Starter Launch my first channel Start from zero — proven templates + mentor." [ref=f1e837]:
                - generic [ref=f1e845]: Tier 1 · Starter
                - generic [ref=f1e846]: Launch my first channel
                - generic [ref=f1e847]: Start from zero — proven templates + mentor.
              - radio "Tier 2 · Growth Scale an existing channel Multiply output, win-back the algorithm." [ref=f1e848]:
                - generic [ref=f1e854]: Tier 2 · Growth
                - generic [ref=f1e855]: Scale an existing channel
                - generic [ref=f1e856]: Multiply output, win-back the algorithm.
              - radio "Tier 3 · Premium Fully automated investment Done-for-you across 7 platforms, passive." [ref=f1e857]:
                - generic [ref=f1e863]: Tier 3 · Premium
                - generic [ref=f1e864]: Fully automated investment
                - generic [ref=f1e865]: Done-for-you across 7 platforms, passive.
          - group [ref=f1e866]:
            - generic [ref=f1e867]:
              - generic [ref=f1e868]: "02"
              - generic [ref=f1e869]:
                - generic [ref=f1e870]: What budget have you reserved for channel setup and production?
                - paragraph [ref=f1e871]: We'll only recommend a plan that matches this number.
            - radiogroup "Budget" [ref=f1e872]:
              - radio "Under $200 Routes to Starter · $197 Starter · $197" [ref=f1e873]:
                - generic [ref=f1e878]:
                  - generic [ref=f1e879]: Under $200
                  - generic [ref=f1e880]: Routes to Starter · $197
                - generic [ref=f1e881]: Starter · $197
              - radio "$500 – $1,000 Routes to Base / Medium plan Base / Medium" [ref=f1e882]:
                - generic [ref=f1e887]:
                  - generic [ref=f1e888]: $500 – $1,000
                  - generic [ref=f1e889]: Routes to Base / Medium plan
                - generic [ref=f1e890]: Base / Medium
              - radio "$1,500 – $5,000+ Routes to Premium / GOD Tier Premium / GOD Tier" [ref=f1e891]:
                - generic [ref=f1e896]:
                  - generic [ref=f1e897]: $1,500 – $5,000+
                  - generic [ref=f1e898]: Routes to Premium / GOD Tier
                - generic [ref=f1e899]: Premium / GOD Tier
          - group [ref=f1e900]:
            - generic [ref=f1e901]:
              - generic [ref=f1e902]: "03"
              - generic [ref=f1e903]:
                - generic [ref=f1e904]: If it's a fit on the call, ready to get started this week?
                - paragraph [ref=f1e905]: This helps us block time for the right plan immediately.
            - radiogroup "Readiness" [ref=f1e906]:
              - radio "Hot lead Yes — ready this week We'll prioritize your slot and reserve onboarding time." [ref=f1e907]:
                - generic [ref=f1e913]: Hot lead
                - generic [ref=f1e914]: Yes — ready this week
                - generic [ref=f1e915]: We'll prioritize your slot and reserve onboarding time.
              - radio "Exploring Not yet — exploring We'll send a self-serve path instead of a hard pitch." [ref=f1e916]:
                - generic [ref=f1e922]: Exploring
                - generic [ref=f1e923]: Not yet — exploring
                - generic [ref=f1e924]: We'll send a self-serve path instead of a hard pitch.
        - generic [ref=f1e925]:
          - button "Schedule my free call" [disabled] [ref=f1e926]
          - link "Prefer to chat first? Discord @instaedit" [ref=f1e931] [cursor=pointer]:
            - /url: https://discord.com/users/1201477873719050332
            - generic [ref=f1e935]:
              - text: Prefer to chat first?
              - generic [ref=f1e936]: Discord @instaedit
        - generic [ref=f1e937]: Three clicks, ~45 seconds. No spam — and we won't surprise-call you.
```

# Test source

```ts
  17  |  *      the canonical noopener windowFeatures. We assert on the
  18  |  *      URL scheme + utm_source substring RATHER than the full URL
  19  |  *      so the test does NOT bake the Calendly placeholder into the
  20  |  *      assertion — when the real Google Appointment slot replaces
  21  |  *      `BOOKING_URL` in web/src/lib/booking.ts, the test stays green.
  22  |  *   5. The booking-tier chip inside the modal renders "Strategy
  23  |  *      Call" (intent="general" — the Hero CTA's default), locking
  24  |  *      the intent argument passed to BookingContext.open().
  25  |  *
  26  |  * Mocking strategy (chosen over a real backend round-trip):
  27  |  *   - window.open is replaced via addInitScript so the call is
  28  |  *     captured FOR ASSERTION but not actually navigated. Real
  29  |  *     navigation would (a) flakify the test on Calendly /
  30  |  *     Google-side availability, (b) cost CI time, and (c)
  31  |  *     "succeed" for the wrong reason.
  32  |  *   - POST /api/v1/booking_events is intercepted via page.route
  33  |  *     so the test is hermetic — no Go backend, no Postgres, no
  34  |  *     rate-limited token bucket to burn.
  35  |  *
  36  |  * Cookie banner: the CookieBanner component renders with
  37  |  * role="dialog" too (per src/components/CookieBanner.tsx). To
  38  |  * avoid a strict-mode locator collision we dismiss the banner
  39  |  * explicitly before asserting on the booking modal.
  40  |  */
  41  | test("Hero CTA opens modal \u2192 3 questions \u2192 Submit fires telemetry POST + window.open", async ({ page }) => {
  42  |   let capturedBody: unknown;
  43  |   let capturedPost = 0;
  44  | 
  45  |   // ── 1. Mock the telemetry POST. The glob matches the browser-
  46  |   //    side URL pattern (Vite dev proxies /api to :8080, but the
  47  |   //    page.route intercept is at the browser layer so the glob
  48  |   //    sees the URL as the SPA sees it.)
  49  |   await page.route("**/api/v1/booking_events", async (route) => {
  50  |     if (route.request().method() !== "POST") {
  51  |       return route.continue();
  52  |     }
  53  |     capturedBody = (() => {
  54  |       try {
  55  |         return JSON.parse(route.request().postData() ?? "{}");
  56  |       } catch {
  57  |         return null;
  58  |       }
  59  |     })();
  60  |     capturedPost += 1;
  61  |     await route.fulfill({
  62  |       status: 200,
  63  |       contentType: "application/json",
  64  |       body: JSON.stringify({ status: "recorded" }),
  65  |     });
  66  |   });
  67  | 
  68  |   // ── 2. Capture window.open so the test does NOT actually
  69  |   //    navigate. addInitScript runs on every page navigation
  70  |   //    BEFORE any module script executes, so React's setTimeout-
  71  |   //    driven window.open call hits the override.
  72  |   await page.addInitScript(() => {
  73  |     interface OpenedEntry {
  74  |       url: string;
  75  |       target: string;
  76  |       features: string;
  77  |     }
  78  |     const opened: OpenedEntry[] = [];
  79  |     (window as unknown as { __opened: OpenedEntry[] }).__opened = opened;
  80  |     const original = window.open;
  81  |     window.open = function (
  82  |       url?: string | URL,
  83  |       target?: string,
  84  |       features?: string,
  85  |     ): Window | null {
  86  |       opened.push({
  87  |         url: url == null ? "" : String(url),
  88  |         target: target ?? "",
  89  |         features: features ?? "",
  90  |       });
  91  |       try {
  92  |         return original.call(window, url, target, features);
  93  |       } catch {
  94  |         return null;
  95  |       }
  96  |     };
  97  |   });
  98  | 
  99  |   // ── 3. Visit the landing page and dismiss the cookie banner so
  100 |   //    the role="dialog" assertion below can't collide with it.
  101 |   await page.goto("/");
  102 |   const cookieAccept = page.getByRole("button", {
  103 |     name: /accept|agree|ok|got it/i,
  104 |   });
  105 |   if (await cookieAccept.first().isVisible().catch(() => false)) {
  106 |     await cookieAccept.first().click();
  107 |   }
  108 | 
  109 |   // ── 4. Click the Hero CTA. The BookingProvider is mounted by
  110 |   //    App.tsx so the modal is available regardless of which page
  111 |   //    we land on; the Hero on / is the entry point marketing
  112 |   //    copy wires users through first.
  113 |   const heroCta = page.getByRole("button", {
  114 |     name: /schedule your free strategy call/i,
  115 |   });
  116 |   await expect(heroCta).toBeVisible({ timeout: 10_000 });
> 117 |   await heroCta.click();
      |                 ^ Error: locator.click: Test timeout of 30000ms exceeded.
  118 | 
  119 |   // ── 5. The BookingProvider modal renders as an ARIA dialog.
  120 |   //    We pin the locator by accessible-name so the cookie banner
  121 |   //    (also role="dialog") cannot collide, and so a future
  122 |   //    second-dialog (e.g. onboarding tip) wouldn't either.
  123 |   const dialog = page.getByRole("dialog", {
  124 |     name: /schedule your free strategy call/i,
  125 |   });
  126 |   await expect(dialog).toBeVisible();
  127 | 
  128 |   // ── 6. Each of the 3 question sections renders the expected
  129 |   //    prompt. We use case-insensitive regex anchors so copy
  130 |   //    tweaks (a trailing "?" or em-dash) don't break the test.
  131 |   await expect(dialog).toContainText(/what is your primary goal right now\?/i);
  132 |   await expect(dialog).toContainText(/what budget/i);
  133 |   await expect(dialog).toContainText(/ready to get started this week\?/i);
  134 | 
  135 |   // ── 7. Tier chip = "Strategy Call" because the Hero CTA opens
  136 |   //    the modal with intent="general" (default). Locks the
  137 |   //    intent argument in BookingContext so a future refactor of
  138 |   //    the Hero CTA's open(...) call would surface in CI.
  139 |   await expect(page.getByTestId("booking-tier-chip")).toContainText(/strategy call/i);
  140 | 
  141 |   // ── 8. Pick the most-populated answers for each closed-set:
  142 |   //    goal=launch, budget=starter, ready=yes (matches the
  143 |   //    "high-intent visitor" journey the marketing copy walks
  144 |   //    through).
  145 |   await dialog
  146 |     .getByRole("radio", { name: /launch my first channel/i })
  147 |     .click();
  148 |   await dialog
  149 |     .getByRole("radio", { name: /under \$200/i })
  150 |     .click();
  151 |   await dialog
  152 |     .getByRole("radio", { name: /yes.*ready this week/i })
  153 |     .click();
  154 | 
  155 |   // Submit becomes enabled. Click + assert both side effects.
  156 |   const submit = dialog.getByRole("button", {
  157 |     name: /schedule my free call/i,
  158 |   });
  159 |   await expect(submit).toBeEnabled();
  160 |   await submit.click();
  161 | 
  162 |   // ── 9. Telemetry POST hit the backend mock with the expected
  163 |   //    payload. expect.poll gives the fire-and-forget promise up
  164 |   //    to 5s to resolve without juggling timers in the test code.
  165 |   await expect
  166 |     .poll(() => capturedPost, { timeout: 5_000 })
  167 |     .toBeGreaterThanOrEqual(1);
  168 |   await expect
  169 |     .poll(() => capturedBody, { timeout: 5_000 })
  170 |     .toEqual({
  171 |       intent: "general",
  172 |       goal: "launch",
  173 |       budget: "starter",
  174 |       ready: "yes",
  175 |     });
  176 | 
  177 |   // ── 10. window.open was invoked. The modal schedules the
  178 |   //     popup in a 220ms setTimeout; expect.poll reads the
  179 |   //     captured array repeatedly so we don't slow the test.
  180 |   await expect
  181 |     .poll(
  182 |       async () =>
  183 |         (
  184 |           await page.evaluate(
  185 |             () =>
  186 |               (
  187 |                 window as unknown as {
  188 |                   __opened: { url: string; target: string; features: string }[];
  189 |                 }
  190 |               ).__opened,
  191 |           )
  192 |         ).length,
  193 |       { timeout: 5_000 },
  194 |     )
  195 |     .toBeGreaterThan(0);
  196 | 
  197 |   const opened = await page.evaluate(() =>
  198 |     (
  199 |       window as unknown as {
  200 |         __opened: { url: string; target: string; features: string }[];
  201 |       }
  202 |     ).__opened,
  203 |   );
  204 |   expect(opened).toHaveLength(1);
  205 | 
  206 |   // ── 11. URL assertions are HOST-AGNOSTIC: assert the scheme
  207 |   //     (any https URL works) + the UTM tag (intent="general"
  208 |   //     maps to utm_source=instagram_landing). When BOOKING_URL
  209 |   //     is replaced with the real Google Appointment slot, this
  210 |   //     test stays green without an edit.
  211 |   expect(opened[0].url).toMatch(/^https:\/\//);
  212 |   expect(opened[0].url).toContain("utm_source=instagram_landing");
  213 | 
  214 |   // ── 12. windowFeatures contract: _blank target + noopener
  215 |   //     + noreferrer. The BookingProvider passes these verbatim
  216 |   //     in window.open's third arg.
  217 |   expect(opened[0].target).toBe("_blank");
```