package svggen

// ─── Umbra — The Eminence in the Dark ─────────────────────────────────────────
//
// Animated elements:
//   1. Purple/violet galaxy swirl background
//   2. Mana swirls rotating around the figure
//   3. Expanding purple energy rings (wave pulses)
//   4. Particle system — floating mana motes
//   5. Dark mage figure with cloaked silhouette
//   6. "Event Horizon" text with cycling glow
//   7. Mysterious fog/mist at the base

import "fmt"

func init() {
	Register(Character{
		Slug:        "umbra",
		Name:        "Umbra — The Eminence in the Dark",
		Description: "A master of void mana who commands the shadows between worlds. The event horizon bends to their will.",
		Attributes: []Attribute{
			{TraitType: "Background", Value: "Violet Galaxy"},
			{TraitType: "Signature Technique", Value: "Event Horizon"},
			{TraitType: "Element", Value: "Void Mana / Shadow"},
			{TraitType: "Faction", Value: "The Obsidian Court"},
			{TraitType: "Series", Value: "Original"},
			{TraitType: "Rarity", Value: "Mythic"},
		},
		Generate: generateUmbra,
	})
}

func generateUmbra() string {
	css := `<style>
@keyframes galaxySpin {
  0%   { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}
@keyframes manaSwirl {
  0%   { transform: rotate(0deg) translateX(-140px) rotate(0deg); opacity: 0.8; }
  100% { transform: rotate(360deg) translateX(-140px) rotate(-360deg); opacity: 0.4; }
}
@keyframes manaSwirl2 {
  0%   { transform: rotate(180deg) translateX(-180px) rotate(-180deg); opacity: 0.6; }
  100% { transform: rotate(540deg) translateX(-180px) rotate(-540deg); opacity: 0.3; }
}
@keyframes ringPulse {
  0%   { r: 20; opacity: 0.8; stroke-width: 3; }
  100% { r: 220; opacity: 0; stroke-width: 0.5; }
}
@keyframes floatMote {
  0%   { transform: translateY(0) translateX(0); opacity: 0; }
  20%  { opacity: 0.8; }
  80%  { opacity: 0.4; }
  100% { transform: translateY(-300px) translateX(50px); opacity: 0; }
}
@keyframes cloakSway {
  0%, 100% { transform: skewX(0deg); }
  25%      { transform: skewX(1.5deg); }
  75%      { transform: skewX(-1.5deg); }
}
@keyframes textGlow {
  0%, 100% { opacity: 0.5; fill: rgba(192,132,252,0.6); }
  50%      { opacity: 1; fill: rgba(216,180,254,1); }
}
@keyframes fogDrift {
  0%   { transform: translateX(-50px); opacity: 0.15; }
  50%  { transform: translateX(50px); opacity: 0.3; }
  100% { transform: translateX(-50px); opacity: 0.15; }
}
</style>`

	body := fmt.Sprintf(`<rect width="1000" height="1000" fill="url(#bgGrad)"/>

  <!-- Galaxy swirl layer -->
  <g transform="translate(500,450)" style="animation: galaxySpin 30s linear infinite;">
    <ellipse cx="0" cy="0" rx="350" ry="120" fill="url(#galaxyGrad)" filter="url(#galaxyBlur)" transform="rotate(-30)"/>
    <ellipse cx="0" cy="0" rx="300" ry="80" fill="url(#galaxyGrad2)" filter="url(#galaxyBlur)" transform="rotate(30)"/>
  </g>

  <!-- Background nebula wisps -->
  <g opacity="0.4">
    <ellipse cx="300" cy="300" rx="200" ry="150" fill="url(#nebulaPurple)" filter="url(#nebulaBlur)">
      <animate attributeName="opacity" values="0.3;0.5;0.3" dur="10s" repeatCount="indefinite"/>
    </ellipse>
    <ellipse cx="700" cy="650" rx="180" ry="120" fill="url(#nebulaIndigo)" filter="url(#nebulaBlur)">
      <animate attributeName="opacity" values="0.2;0.4;0.2" dur="12s" repeatCount="indefinite" begin="4s"/>
    </ellipse>
  </g>

  <!-- Stars -->
  <g opacity="0.5">
    <circle cx="80" cy="120" r="1.5" fill="#fff"><animate attributeName="opacity" values="0.3;1;0.3" dur="2.5s" repeatCount="indefinite"/></circle>
    <circle cx="920" cy="200" r="2" fill="#fff"><animate attributeName="opacity" values="0.5;1;0.5" dur="3s" repeatCount="indefinite"/></circle>
    <circle cx="150" cy="700" r="1" fill="#fff"><animate attributeName="opacity" values="0.2;0.9;0.2" dur="4s" repeatCount="indefinite"/></circle>
    <circle cx="850" cy="750" r="1.5" fill="#fff"><animate attributeName="opacity" values="0.4;0.8;0.4" dur="3.5s" repeatCount="indefinite"/></circle>
    <circle cx="200" cy="400" r="1" fill="#fff"><animate attributeName="opacity" values="0.3;0.7;0.3" dur="2.8s" repeatCount="indefinite"/></circle>
    <circle cx="780" cy="450" r="1.2" fill="#fff"><animate attributeName="opacity" values="0.5;1;0.5" dur="3.2s" repeatCount="indefinite"/></circle>
  </g>

  <!-- Mana swirl orbital paths -->
  <g transform="translate(500,480)">
    <!-- Orbital path rings -->
    <ellipse cx="0" cy="0" rx="160" ry="60" fill="none" stroke="rgba(168,85,247,0.15)" stroke-width="1" transform="rotate(-15)"/>
    <ellipse cx="0" cy="0" rx="200" ry="70" fill="none" stroke="rgba(192,132,252,0.1)" stroke-width="1" transform="rotate(25)"/>

    <!-- Mana orbs orbiting -->
    <g style="animation: manaSwirl 6s linear infinite;">
      <circle cx="0" cy="0" r="6" fill="#a78bfa" filter="url(#manaGlow)"/>
    </g>
    <g style="animation: manaSwirl 7s linear infinite;">
      <circle cx="0" cy="0" r="4" fill="#c084fc" filter="url(#manaGlow)"/>
    </g>
    <g style="animation: manaSwirl2 9s linear infinite;">
      <circle cx="0" cy="0" r="5" fill="#818cf8" filter="url(#manaGlow)"/>
    </g>
    <g style="animation: manaSwirl2 11s linear infinite;">
      <circle cx="0" cy="0" r="3" fill="#e879f9" filter="url(#manaGlow)"/>
    </g>

    <!-- Energy wave rings -->
    <circle cx="0" cy="0" fill="none" stroke="#a78bfa" stroke-width="2">
      <animate attributeName="r" values="10;200" dur="5s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.6;0" dur="5s" repeatCount="indefinite"/>
    </circle>
    <circle cx="0" cy="0" fill="none" stroke="#c084fc" stroke-width="1.5">
      <animate attributeName="r" values="10;200" dur="5s" begin="1.5s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.5;0" dur="5s" begin="1.5s" repeatCount="indefinite"/>
    </circle>
    <circle cx="0" cy="0" fill="none" stroke="#818cf8" stroke-width="1.5">
      <animate attributeName="r" values="10;200" dur="5s" begin="3s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.4;0" dur="5s" begin="3s" repeatCount="indefinite"/>
    </circle>
  </g>

  <!-- Umbra figure - cloaked mage -->
  <g transform="translate(500,660)">
    <!-- Dark aura -->
    <ellipse cx="0" cy="0" rx="120" ry="160" fill="url(#auraGrad)" opacity="0.3" filter="url(#auraBlur)">
      <animate attributeName="opacity" values="0.2;0.4;0.2" dur="4s" repeatCount="indefinite"/>
    </ellipse>

    <!-- Cloak / robe -->
    <path d="M-90,20 C-100,-80 -70,-180 -30,-220 C0,-240 30,-230 50,-200 C80,-160 95,-80 85,20 Z" fill="url(#cloakGrad)"/>

    <!-- Cloak sway animation -->
    <g style="animation: cloakSway 4s ease-in-out infinite;">
      <!-- Inner cloak detail -->
      <path d="M-70,10 C-80,-70 -50,-160 -15,-200 C5,-215 20,-205 35,-180 C60,-140 75,-70 65,10 Z" fill="#0a0a1a" opacity="0.6"/>
    </g>

    <!-- Collar / mantle -->
    <path d="M-40,-200 C-50,-210 -30,-230 0,-235 C30,-230 50,-210 40,-200 C20,-195 -20,-195 -40,-200 Z" fill="#2a1a3a"/>

    <!-- Head silhouette -->
    <ellipse cx="0" cy="-210" rx="30" ry="35" fill="#0a0a0a"/>

    <!-- Eyes - glowing violet -->
    <ellipse cx="-10" cy="-215" rx="8" ry="4" fill="#c084fc" filter="url(#manaGlow)">
      <animate attributeName="opacity" values="0.6;1;0.6" dur="2s" repeatCount="indefinite"/>
    </ellipse>
    <ellipse cx="10" cy="-215" rx="8" ry="4" fill="#c084fc" filter="url(#manaGlow)">
      <animate attributeName="opacity" values="0.6;1;0.6" dur="2s" repeatCount="indefinite" begin="0.3s"/>
    </ellipse>
    <ellipse cx="-10" cy="-215" rx="3" ry="1.5" fill="#fff"/>
    <ellipse cx="10" cy="-215" rx="3" ry="1.5" fill="#fff"/>

    <!-- Right arm raised (casting) -->
    <path d="M40,-180 C70,-200 100,-220 120,-260" stroke="#0a0a0a" stroke-width="18" fill="none" stroke-linecap="round"/>
    <!-- Hand mana orb -->
    <circle cx="120" cy="-260" r="20" fill="url(#manaOrbGrad)" filter="url(#manaGlow)">
      <animate attributeName="r" values="18;25;18" dur="2s" repeatCount="indefinite"/>
    </circle>
    <!-- Mana tendrils from hand -->
    <path d="M120,-260 Q140,-280 130,-300" stroke="rgba(192,132,252,0.4)" stroke-width="2" fill="none" opacity="0">
      <animate attributeName="opacity" values="0.3;0.7;0.3" dur="1.5s" repeatCount="indefinite"/>
    </path>
    <path d="M120,-260 Q150,-250 145,-275" stroke="rgba(129,140,248,0.4)" stroke-width="1.5" fill="none" opacity="0">
      <animate attributeName="opacity" values="0.2;0.6;0.2" dur="1.8s" begin="0.5s" repeatCount="indefinite"/>
    </path>

    <!-- Left arm at side -->
    <path d="M-40,-180 C-70,-190 -90,-170 -100,-150" stroke="#0a0a0a" stroke-width="16" fill="none" stroke-linecap="round"/>

    <!-- Chest / robe details -->
    <path d="M-20,-160 L0,-140 L20,-160" stroke="rgba(192,132,252,0.2)" stroke-width="1" fill="none"/>
    <path d="M-15,-140 L0,-120 L15,-140" stroke="rgba(192,132,252,0.15)" stroke-width="1" fill="none"/>
  </g>

  <!-- Floating mana motes (particles) -->
  <g>
    %s
  </g>

  <!-- Base fog -->
  <g>
    <ellipse cx="300" cy="850" rx="200" ry="40" fill="rgba(88,28,135,0.2)" filter="url(#nebulaBlur)" style="animation: fogDrift 8s ease-in-out infinite;"/>
    <ellipse cx="700" cy="860" rx="220" ry="35" fill="rgba(76,29,149,0.15)" filter="url(#nebulaBlur)" style="animation: fogDrift 10s ease-in-out infinite 2s;"/>
  </g>

  <!-- Foreground mist -->
  <rect x="0" y="850" width="1000" height="150" fill="url(#bottomMist)" opacity="0">
    <animate attributeName="opacity" values="0.2;0.35;0.2" dur="6s" repeatCount="indefinite"/>
  </rect>

  <!-- Character name -->
  <g transform="translate(500,945)">
    <text text-anchor="middle" font-family="Georgia, serif" font-size="22" font-style="italic" letter-spacing="6" fill="rgba(192,132,252,0.6)" style="animation: textGlow 3s ease-in-out infinite;">
      ✦ The Eminence in the Dark ✦
    </text>
  </g>`,

		// Generate mana motes
		func() string {
			var out string
			motes := []struct{ x, y, r, dur, delay, dx float64 }{
				{200, 700, 2.5, 5, 0, 30}, {350, 650, 2, 6, 0.5, -20},
				{650, 680, 3, 7, 1, 40}, {780, 720, 2, 5.5, 1.5, -30},
				{150, 500, 1.5, 8, 0.3, 20}, {850, 550, 2, 6.5, 2, -25},
				{300, 450, 2, 7, 2.5, 35}, {700, 480, 1.5, 5.5, 0.8, -15},
				{250, 800, 2, 6, 3, 25}, {750, 800, 2.5, 7.5, 3.5, -35},
			}
			for _, m := range motes {
				colors := []string{"#a78bfa", "#c084fc", "#818cf8", "#e879f9"}
				color := colors[int(m.x+float64(len(motes)))%len(colors)]
				out += fmt.Sprintf(`
    <circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" opacity="0" filter="url(#manaGlow)">
      <animate attributeName="cy" values="%.1f;%.1f" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
      <animate attributeName="cx" values="%.1f;%.1f" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0;0.7;0" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
    </circle>`, m.x, m.y, m.r, color, m.y, m.y-400, m.dur, m.delay, m.x, m.x+m.dx, m.dur, m.delay, m.dur, m.delay)
			}
			return out
		}(),
	)

	svg := SVG(css, body)
	svg = svg[:len(svg)-6] +
		"\n" + RadialGrad("bgGrad", 0.5, 0.5, 0.9,
			Stop("0%", "#0d001a", 1)+
				Stop("25%", "#15002a", 1)+
				Stop("55%", "#1a0533", 1)+
				Stop("100%", "#05000a", 1)) +
		"\n" + RadialGrad("galaxyGrad", 0.5, 0.5, 0.6,
			Stop("0%", "rgba(147,51,234,0.4)", 1)+
				Stop("50%", "rgba(88,28,135,0.2)", 1)+
				Stop("100%", "rgba(88,28,135,0)", 1)) +
		"\n" + RadialGrad("galaxyGrad2", 0.5, 0.5, 0.6,
			Stop("0%", "rgba(99,102,241,0.3)", 1)+
				Stop("50%", "rgba(76,29,149,0.15)", 1)+
				Stop("100%", "rgba(76,29,149,0)", 1)) +
		"\n" + RadialGrad("nebulaPurple", 0.5, 0.5, 0.6,
			Stop("0%", "rgba(147,51,234,0.2)", 1)+
				Stop("100%", "rgba(147,51,234,0)", 1)) +
		"\n" + RadialGrad("nebulaIndigo", 0.5, 0.5, 0.6,
			Stop("0%", "rgba(79,70,229,0.15)", 1)+
				Stop("100%", "rgba(79,70,229,0)", 1)) +
		"\n" + RadialGrad("cloakGrad", 0.5, 0.4, 0.8,
			Stop("0%", "#1a0a2e", 1)+
				Stop("50%", "#0d001a", 1)+
				Stop("100%", "#000005", 1)) +
		"\n" + RadialGrad("auraGrad", 0.5, 0.5, 0.6,
			Stop("0%", "rgba(147,51,234,0.3)", 1)+
				Stop("100%", "rgba(147,51,234,0)", 1)) +
		"\n" + RadialGrad("manaOrbGrad", 0.5, 0.5, 0.5,
			Stop("0%", "#e9d5ff", 1)+
				Stop("30%", "#a78bfa", 1)+
				Stop("100%", "rgba(88,28,135,0)", 1)) +
		"\n" + LinearGrad("bottomMist", 0, 0, 0, 1,
			Stop("0%", "rgba(88,28,135,0)", 1)+
				Stop("100%", "rgba(88,28,135,0.2)", 1)) +
		"\n" + GlowFilter("manaGlow", "#c084fc", 5) +
		`<filter id="galaxyBlur" x="-50%" y="-50%" width="200%" height="200%">
  <feGaussianBlur in="SourceGraphic" stdDeviation="50"/>
</filter>
<filter id="nebulaBlur" x="-50%" y="-50%" width="200%" height="200%">
  <feGaussianBlur in="SourceGraphic" stdDeviation="30"/>
</filter>
<filter id="auraBlur" x="-50%" y="-50%" width="200%" height="200%">
  <feGaussianBlur in="SourceGraphic" stdDeviation="20"/>
</filter>` +
		"\n</svg>"
	return svg
}
