package svggen

// ─── Raven — Shadow of the Crimson Moon ──────────────────────────────────────
//
// Animated elements:
//   1. Crimson moon with pulsing glow
//   2. Three crows flying in elliptical orbits at different speeds
//   3. Shadow particles floating upward
//   4. Blood-mist wisps with slow opacity cycling
//   5. Pulsing crimson eye in the shadow silhouette
//   6. Falling feather particles
//   7. Dark vortex ring rotating

import "fmt"

func init() {
	Register(Character{
		Slug:        "raven",
		Name:        "Raven — Shadow of the Crimson Moon",
		Description: "A solitary figure beneath a blood-red eclipse, wielding abyssal flame. The crows carry whispers of forgotten truths.",
		Attributes: []Attribute{
			{TraitType: "Background", Value: "Crimson Eclipse"},
			{TraitType: "Signature Technique", Value: "Abyssal Flare"},
			{TraitType: "Element", Value: "Shadow / Crimson Flame"},
			{TraitType: "Faction", Value: "The Veil Order"},
			{TraitType: "Series", Value: "Original"},
			{TraitType: "Rarity", Value: "Mythic"},
		},
		Generate: generateRaven,
	})
}

func generateRaven() string {
	css := fmt.Sprintf(`<style>
@keyframes moonPulse {
  0%%, 100%% { r: 120; opacity: 0.85; }
  50%%       { r: 135; opacity: 1; }
}
@keyframes moonGlow {
  0%%, 100%% { opacity: 0.4; r: 140; }
  50%%       { opacity: 0.7; r: 160; }
}
@keyframes crowFly {
  0%%   { transform: rotate(0deg) translateX(-280px) rotate(0deg); }
  100%% { transform: rotate(360deg) translateX(-280px) rotate(-360deg); }
}
@keyframes crowFly2 {
  0%%   { transform: rotate(120deg) translateX(-320px) rotate(-120deg); }
  100%% { transform: rotate(480deg) translateX(-320px) rotate(-480deg); }
}
@keyframes crowFly3 {
  0%%   { transform: rotate(240deg) translateX(-240px) rotate(-240deg); }
  100%% { transform: rotate(600deg) translateX(-240px) rotate(-600deg); }
}
@keyframes floatUp {
  0%%   { transform: translateY(0px) scale(1); opacity: 0; }
  20%%  { opacity: 0.6; }
  80%%  { opacity: 0.3; }
  100%% { transform: translateY(-400px) scale(0.5); opacity: 0; }
}
@keyframes eyePulse {
  0%%, 100%% { opacity: 0.7; }
  50%%       { opacity: 1; }
}
@keyframes mistWave {
  0%%   { transform: translateX(0) scaleY(1); opacity: 0.15; }
  25%%  { transform: translateX(30px) scaleY(1.2); opacity: 0.25; }
  50%%  { transform: translateX(0) scaleY(1); opacity: 0.15; }
  75%%  { transform: translateX(-30px) scaleY(1.2); opacity: 0.25; }
  100%% { transform: translateX(0) scaleY(1); opacity: 0.15; }
}
@keyframes featherFall {
  0%%   { transform: translateY(0) rotate(0deg); opacity: 0; }
  10%%  { opacity: 0.8; }
  90%%  { opacity: 0.4; }
  100%% { transform: translateY(500px) rotate(720deg); opacity: 0; }
}
@keyframes vortexSpin {
  0%%   { transform: rotate(0deg); opacity: 0.15; }
  50%%  { opacity: 0.3; }
  100%% { transform: rotate(360deg); opacity: 0.15; }
}
@keyframes textGlow {
  0%%, 100%% { opacity: 0.6; }
  50%%       { opacity: 1; }
}
</style>`)

	body := fmt.Sprintf(`<rect width="1000" height="1000" fill="url(#bgGrad)"/>

  <!-- Background stars -->
  <g opacity="0.3">
    <circle cx="120" cy="80" r="1.5" fill="#fff"><animate attributeName="opacity" values="0.3;1;0.3" dur="3s" repeatCount="indefinite"/></circle>
    <circle cx="850" cy="150" r="1" fill="#fff"><animate attributeName="opacity" values="0.5;1;0.5" dur="2.5s" repeatCount="indefinite"/></circle>
    <circle cx="200" cy="300" r="1.2" fill="#fff"><animate attributeName="opacity" values="0.2;0.9;0.2" dur="4s" repeatCount="indefinite"/></circle>
    <circle cx="750" cy="400" r="1" fill="#fff"><animate attributeName="opacity" values="0.4;1;0.4" dur="3.5s" repeatCount="indefinite"/></circle>
    <circle cx="100" cy="600" r="1.3" fill="#fff"><animate attributeName="opacity" values="0.3;0.8;0.3" dur="2.8s" repeatCount="indefinite"/></circle>
    <circle cx="900" cy="700" r="1" fill="#fff"><animate attributeName="opacity" values="0.5;0.9;0.5" dur="3.2s" repeatCount="indefinite"/></circle>
  </g>

  <!-- Crimson moon glow aura -->
  <circle cx="500" cy="350" fill="url(#moonGlow)" opacity="0">
    <animate attributeName="opacity" values="0.3;0.6;0.3" dur="4s" repeatCount="indefinite"/>
    <animate attributeName="r" values="140;160;140" dur="4s" repeatCount="indefinite"/>
  </circle>

  <!-- Crimson moon -->
  <circle cx="500" cy="350" fill="url(#moonGrad)" opacity="0">
    <animate attributeName="opacity" values="0.85;1;0.85" dur="4s" repeatCount="indefinite"/>
    <animate attributeName="r" values="120;135;120" dur="4s" repeatCount="indefinite"/>
  </circle>

  <!-- Moon crater details -->
  <circle cx="460" cy="320" r="25" fill="rgba(180,30,30,0.15)"/>
  <circle cx="540" cy="370" r="18" fill="rgba(180,30,30,0.12)"/>
  <circle cx="480" cy="390" r="12" fill="rgba(180,30,30,0.1)"/>

  <!-- Moon crescent shadow -->
  <path d="M500 230 A120 120 0 0 0 500 470 A100 100 0 0 1 500 230Z" fill="rgba(80,10,10,0.4)"/>

  <!-- Vortex ring -->
  <g transform="translate(500,500)" style="animation: vortexSpin 20s linear infinite;">
    <ellipse cx="0" cy="0" rx="200" ry="60" fill="none" stroke="rgba(180,40,40,0.15)" stroke-width="4">
      <animateTransform attributeName="transform" type="rotate" from="0" to="360" dur="20s" repeatCount="indefinite"/>
    </ellipse>
    <ellipse cx="0" cy="0" rx="200" ry="60" fill="none" stroke="rgba(200,50,50,0.08)" stroke-width="2" transform="rotate(90)"/>
  </g>

  <!-- Flying crows -->
  <g transform="translate(500,350)">
    <g style="animation: crowFly 6s linear infinite;">
      <path d="M-15,0 L-5,-8 L0,0 L5,-6 L15,0 L5,2 L0,-2 L-5,2 Z" fill="#0a0a0a"/>
    </g>
    <g style="animation: crowFly2 8s linear infinite;">
      <path d="M-12,0 L-4,-6 L0,0 L4,-5 L12,0 L4,2 L0,-1 L-4,2 Z" fill="#1a1a1a"/>
    </g>
    <g style="animation: crowFly3 10s linear infinite;">
      <path d="M-10,0 L-3,-5 L0,0 L3,-4 L10,0 L3,1 L0,-1 L-3,1 Z" fill="#2a2a2a"/>
    </g>
  </g>

  <!-- Shadow figure silhouette -->
  <g transform="translate(500,680)">
    <!-- Cloak / body -->
    <path d="M-80,0 C-90,-120 -60,-200 -20,-220 C10,-230 30,-220 50,-200 C80,-170 90,-100 80,0 Z" fill="url(#shadowGrad)"/>
    <!-- Head -->
    <ellipse cx="0" cy="-200" rx="35" ry="40" fill="#0a0a0a"/>
    <!-- Shoulder pauldrons -->
    <path d="M-80,0 C-95,-30 -85,-60 -70,-70 L-60,-60 C-75,-50 -80,-30 -75,0 Z" fill="#1a1a1a"/>
    <path d="M80,0 C95,-30 85,-60 70,-70 L60,-60 C75,-50 80,-30 75,0 Z" fill="#1a1a1a"/>
    <!-- Chest armor -->
    <path d="M-30,-160 L-25,-80 L25,-80 L30,-160 Z" fill="#1a1a1a" stroke="rgba(200,50,50,0.3)" stroke-width="1"/>

    <!-- Pulsing crimson eye -->
    <ellipse cx="0" cy="-195" rx="12" ry="6" fill="#ff2020" filter="url(#crimsonGlow)" style="animation: eyePulse 2s ease-in-out infinite;"/>
    <ellipse cx="0" cy="-195" rx="4" ry="2" fill="#ff6060"/>

    <!-- Left eye (dim) -->
    <ellipse cx="-18" cy="-195" rx="8" ry="4" fill="#ff1010" filter="url(#crimsonGlow)" opacity="0.4"/>

    <!-- Energy lines on cloak -->
    <path d="M-40,-100 C-30,-80 -20,-60 -10,-40" stroke="rgba(200,50,50,0.3)" stroke-width="1" fill="none"/>
    <path d="M40,-100 C30,-80 20,-60 10,-40" stroke="rgba(200,50,50,0.3)" stroke-width="1" fill="none"/>
  </g>

  <!-- Blood mist wisps -->
  <g>
    <path d="M300,800 Q350,750 320,700 Q290,650 330,600" fill="none" stroke="rgba(180,40,40,0.2)" stroke-width="30" stroke-linecap="round" filter="url(#mistBlur)" opacity="0">
      <animate attributeName="opacity" values="0;0.25;0.15;0.25;0" dur="8s" repeatCount="indefinite"/>
    </path>
    <path d="M700,820 Q650,770 680,720 Q710,670 670,620" fill="none" stroke="rgba(180,40,40,0.2)" stroke-width="25" stroke-linecap="round" filter="url(#mistBlur)" opacity="0">
      <animate attributeName="opacity" values="0;0.2;0.1;0.2;0" dur="10s" repeatCount="indefinite" begin="2s"/>
    </path>
  </g>

  <!-- Floating shadow particles -->
  <g>
    %s
  </g>

  <!-- Falling feathers -->
  <g>
    %s
  </g>

  <!-- Top mist overlay -->
  <rect x="0" y="0" width="1000" height="200" fill="url(#topMist)" opacity="0.3">
    <animate attributeName="opacity" values="0.2;0.4;0.2" dur="6s" repeatCount="indefinite"/>
  </rect>

  <!-- Foreground fog -->
  <rect x="0" y="850" width="1000" height="150" fill="url(#fogGrad)" opacity="0">
    <animate attributeName="opacity" values="0.2;0.4;0.2" dur="5s" repeatCount="indefinite"/>
  </rect>

  <!-- Character name -->
  <g transform="translate(500,940)">
    <text text-anchor="middle" fill="rgba(200,50,50,0.6)" font-family="Georgia, serif" font-size="22" font-style="italic" letter-spacing="6" style="animation: textGlow 3s ease-in-out infinite;">
      ✦ Shadow of the Crimson Moon ✦
    </text>
  </g>`,
		// Generate particles
		func() string {
			var out string
			particles := [][5]float64{
				{200, 750, 2, 1.5, 0.5}, {350, 800, 3, 2, 0.8}, {650, 780, 2, 1.8, 0.6},
				{780, 820, 2.5, 1.2, 0.4}, {150, 700, 1.5, 2.5, 0.7}, {450, 850, 2, 1, 0.9},
				{550, 790, 3, 2.2, 0.5}, {300, 720, 2, 1.5, 1.1}, {700, 750, 1.5, 1.8, 0.3},
				{250, 830, 2.5, 1, 0.6},
			}
			for i, p := range particles {
				delay := float64(i) * 0.7
				out += fmt.Sprintf(`
    <circle cx="%.1f" cy="%.1f" r="%.1f" fill="rgba(180,40,40,0.6)" opacity="0">
      <animate attributeName="cy" values="%.1f;%.1f" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0;0.6;0" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
    </circle>`, p[0], p[1], p[2], p[1], p[1]-350, p[3]+2, delay, p[3]+2, delay)
			}
			return out
		}(),

		// Generate feathers
		func() string {
			var out string
			feathers := [][4]float64{
				{400, 200, 0.5, 0},
				{550, 250, 0.7, 1},
				{320, 180, 0.3, 2},
				{680, 220, 0.6, 3},
				{450, 300, 0.4, 4},
			}
			for _, f := range feathers {
				out += fmt.Sprintf(`
    <g transform="translate(%.1f,%.1f)" opacity="0">
      <path d="M0,0 C-5,-3 -8,-1 -5,4 C-2,9 0,14 0,14 C0,14 2,9 5,4 C8,-1 5,-3 0,0Z" fill="rgba(40,10,10,0.7)"/>
      <animateTransform attributeName="transform" type="translate" values="%.1f,%.1f;%.1f,%.1f" dur="%ds" begin="%ds" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0;0.7;0" dur="%ds" begin="%ds" repeatCount="indefinite"/>
    </g>`, f[0], f[1], f[0], f[1], f[0]+30, f[1]+400, 5, f[3], 5, f[3])
			}
			return out
		}(),
	)

	svg := SVG(css, body)
	svg = svg[:len(svg)-6] + // close </svg> before adding defs
		"\n" + RadialGrad("bgGrad", 0.5, 0.6, 0.9,
			Stop("0%", "#0a0008", 1)+
				Stop("30%", "#1a0008", 1)+
				Stop("60%", "#2a0500", 1)+
				Stop("100%", "#050005", 1)) +
		"\n" + RadialGrad("moonGrad", 0.5, 0.35, 0.4,
			Stop("0%", "#ff3030", 1)+
				Stop("40%", "#cc1010", 1)+
				Stop("100%", "#660000", 1)) +
		"\n" + RadialGrad("moonGlow", 0.5, 0.35, 0.3,
			Stop("0%", "rgba(255,50,50,0.3)", 1)+
				Stop("100%", "rgba(255,50,50,0)", 1)) +
		"\n" + RadialGrad("shadowGrad", 0.5, 0.6, 0.8,
			Stop("0%", "#1a1a1a", 1)+
				Stop("60%", "#0a0a0a", 1)+
				Stop("100%", "#000000", 1)) +
		"\n" + LinearGrad("topMist", 0, 0, 0, 1,
			Stop("0%", "rgba(100,10,10,0.4)", 1)+
				Stop("100%", "rgba(100,10,10,0)", 1)) +
		"\n" + LinearGrad("fogGrad", 0, 0, 0, 1,
			Stop("0%", "rgba(80,10,10,0)", 1)+
				Stop("100%", "rgba(80,10,10,0.3)", 1)) +
		"\n" + GlowFilter("crimsonGlow", "#ff2020", 4) +
		fmt.Sprintf(`<filter id="mistBlur" x="-50%%" y="-50%%" width="200%%" height="200%%">
  <feGaussianBlur in="SourceGraphic" stdDeviation="15"/>
</filter>`) +
		"\n</svg>"
	return svg
}
