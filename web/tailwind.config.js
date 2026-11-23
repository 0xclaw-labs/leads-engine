/** @type {import('tailwindcss').Config} */
module.exports = {
    darkMode: ['class'],
    content: ['./src/**/*.{js,ts,jsx,tsx,mdx}'], corePlugins: { preflight: false }, theme: {
    	extend: {
    		borderRadius: {
    			lg: 'var(--ui-radius)',
    			md: 'calc(var(--ui-radius) - 2px)',
    			sm: 'calc(var(--ui-radius) - 4px)'
    		},
    		colors: {
    			background: 'var(--ui-background)',
    			foreground: 'var(--ui-foreground)',
    			card: {
    				DEFAULT: 'var(--ui-card)',
    				foreground: 'var(--ui-card-foreground)'
    			},
    			popover: {
    				DEFAULT: 'var(--ui-popover)',
    				foreground: 'var(--ui-popover-foreground)'
    			},
    			primary: {
    				DEFAULT: 'var(--ui-primary)',
    				foreground: 'var(--ui-primary-foreground)'
    			},
    			secondary: {
    				DEFAULT: 'var(--ui-secondary)',
    				foreground: 'var(--ui-secondary-foreground)'
    			},
    			muted: {
    				DEFAULT: 'var(--ui-muted)',
    				foreground: 'var(--ui-muted-foreground)'
    			},
    			accent: {
    				DEFAULT: 'var(--ui-accent)',
    				foreground: 'var(--ui-accent-foreground)'
    			},
    			destructive: {
				DEFAULT: 'var(--ui-destructive)',
				foreground: 'var(--ui-destructive-foreground)'
			},
    			border: 'var(--ui-border)',
    			input: 'var(--ui-input)',
    			ring: 'var(--ui-ring)',
    			chart: {
    				'1': 'var(--ui-chart-1)',
    				'2': 'var(--ui-chart-2)',
    				'3': 'var(--ui-chart-3)',
    				'4': 'var(--ui-chart-4)',
    				'5': 'var(--ui-chart-5)'
    			},
    			sidebar: {
    				DEFAULT: 'var(--ui-sidebar)',
    				foreground: 'var(--ui-sidebar-foreground)',
    				primary: 'var(--ui-sidebar-primary)',
    				'primary-foreground': 'var(--ui-sidebar-primary-foreground)',
    				accent: 'var(--ui-sidebar-accent)',
    				'accent-foreground': 'var(--ui-sidebar-accent-foreground)',
    				border: 'var(--ui-sidebar-border)',
    				ring: 'var(--ui-sidebar-ring)'
    			}
    		},
    		keyframes: {
    			'accordion-down': {
    				from: {
    					height: '0'
    				},
    				to: {
    					height: 'var(--radix-accordion-content-height)'
    				}
    			},
    			'accordion-up': {
    				from: {
    					height: 'var(--radix-accordion-content-height)'
    				},
    				to: {
    					height: '0'
    				}
    			}
    		},
    		animation: {
    			'accordion-down': 'accordion-down 0.2s ease-out',
    			'accordion-up': 'accordion-up 0.2s ease-out'
    		}
    	}
    }, plugins: [require("tailwindcss-animate")] }
