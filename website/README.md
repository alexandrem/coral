# Coral Landing Page

A modern, dark-themed landing page for Coral - Application Intelligence Mesh.

## Features

- **Dark theme** inspired by SignOz
- **Responsive design** works on all devices
- **Punchy value proposition** highlighting Coral's key differentiators
- **Interactive terminal demo** showing the developer experience
- **Clear feature sections** explaining observe, debug, and control capabilities
- **Simple architecture diagram** visualizing the three-tier design
- **Getting started guide** with step-by-step instructions

## Viewing Locally

Simply open `index.html` in your browser:

```bash
# Using Python's built-in HTTP server
cd website
python3 -m http.server 8000

# Or using any other static file server
npx serve .
```

Then navigate to `http://localhost:8000` in your browser.

## Deployment

This is a static site that can be deployed to:

- **GitHub Pages**: Push to a `gh-pages` branch
- **Netlify**: Drag and drop the `website` folder
- **Vercel**: Connect your repository
- **Any static hosting**: Upload the files

## Structure

- `index.html` - Main landing page
- `style.css` - Dark theme styles and responsive design
- `README.md` - This file

## Customization

### Colors

The color scheme is defined in CSS variables in `style.css`:

```css
:root {
    --bg-primary: #0b0b0f;
    --accent-primary: #6366f1;
    --accent-secondary: #8b5cf6;
    /* ... */
}
```

### Content

All content can be edited directly in `index.html`. Key sections:

- Hero section with main value proposition
- Problem/solution section
- Features grid
- Differentiators
- How it works
- Getting started

## Design Inspiration

The design takes inspiration from modern dark-themed developer tools like:

- SignOz - Dark theme and card-based layout
- Vercel - Clean typography and gradients
- Tailwind CSS - Modern color palette and spacing

## License

Same as the main Coral project.
